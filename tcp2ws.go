// Tcp over WebSocket (tcp2ws)
// 基于ws的内网穿透工具
// Sparkle 20210430
// v11.3

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/miekg/dns"
)

type tcp2wsSparkle struct {
	isUdp            bool
	udpConn          *net.UDPConn
	tcpConn          net.Conn
	uuid             string
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.RWMutex
	wsConn           *websocket.Conn
	udpAddr          *net.UDPAddr
	del              bool
	t                int64
	messageType      int
	writeMu          sync.Mutex
	buf              []queuedMessage
	reconnectMu      sync.Mutex
	reconnecting     bool
	reconnectAuto    bool
	reconnectPending bool
	retryDelay       time.Duration
	retryAt          time.Time
}

type queuedMessage struct {
	messageType int
	data        []byte
}

type wsDialFunc func(context.Context, string) (*websocket.Conn, error)

var (
	tcpAddr    string
	wsAddr     string
	wsAddrIp   string
	wsAddrPort = ""
	isServer   bool
	connMap    map[string]*tcp2wsSparkle = make(map[string]*tcp2wsSparkle)
	// go的map不是线程安全的 读写冲突就会直接exit
	connMapLock *sync.RWMutex = new(sync.RWMutex)
)

var (
	initialReconnectDelay = 250 * time.Millisecond
	maxReconnectDelay     = 30 * time.Second
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func getConn(uuid string) (*tcp2wsSparkle, bool) {
	connMapLock.RLock()
	defer connMapLock.RUnlock()
	conn, haskey := connMap[uuid]
	return conn, haskey
}

func setConn(uuid string, conn *tcp2wsSparkle) {
	connMapLock.Lock()
	defer connMapLock.Unlock()
	connMap[uuid] = conn
}

func connMapSnapshot() map[string]*tcp2wsSparkle {
	connMapLock.RLock()
	defer connMapLock.RUnlock()

	snapshot := make(map[string]*tcp2wsSparkle, len(connMap))
	for uuid, conn := range connMap {
		snapshot[uuid] = conn
	}
	return snapshot
}

func newTcp2wsSparkle(isUdp bool, udpConn *net.UDPConn, tcpConn net.Conn, wsConn *websocket.Conn, uuid string) *tcp2wsSparkle {
	ctx, cancel := context.WithCancel(context.Background())
	return &tcp2wsSparkle{
		isUdp:       isUdp,
		udpConn:     udpConn,
		tcpConn:     tcpConn,
		wsConn:      wsConn,
		uuid:        uuid,
		ctx:         ctx,
		cancel:      cancel,
		t:           time.Now().Unix(),
		messageType: websocket.BinaryMessage,
	}
}

func (conn *tcp2wsSparkle) isDeleted() bool {
	conn.mu.RLock()
	defer conn.mu.RUnlock()
	return conn.del
}

func (conn *tcp2wsSparkle) currentWS() *websocket.Conn {
	conn.mu.RLock()
	defer conn.mu.RUnlock()
	return conn.wsConn
}

func (conn *tcp2wsSparkle) currentWSIs(wsConn *websocket.Conn) bool {
	conn.mu.RLock()
	defer conn.mu.RUnlock()
	return !conn.del && conn.wsConn == wsConn
}

func (conn *tcp2wsSparkle) clearWSIfCurrent(wsConn *websocket.Conn) bool {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.del || conn.wsConn != wsConn {
		return false
	}
	conn.wsConn = nil
	return true
}

func (conn *tcp2wsSparkle) updateActivity() {
	conn.mu.Lock()
	conn.t = time.Now().Unix()
	conn.mu.Unlock()
}

func (conn *tcp2wsSparkle) lastActivity() int64 {
	conn.mu.RLock()
	defer conn.mu.RUnlock()
	return conn.t
}

func (conn *tcp2wsSparkle) setUDPAddr(addr *net.UDPAddr) {
	conn.mu.Lock()
	conn.udpAddr = addr
	conn.mu.Unlock()
}

func (conn *tcp2wsSparkle) currentUDPAddr() *net.UDPAddr {
	conn.mu.RLock()
	defer conn.mu.RUnlock()
	if conn.udpAddr == nil {
		return nil
	}
	addr := *conn.udpAddr
	return &addr
}

func (conn *tcp2wsSparkle) setMessageType(messageType int) {
	conn.mu.Lock()
	conn.messageType = messageType
	conn.mu.Unlock()
}

func (conn *tcp2wsSparkle) currentMessageType() int {
	conn.mu.RLock()
	defer conn.mu.RUnlock()
	return conn.messageType
}

func deleteConn(uuid string) {
	if conn, haskey := getConn(uuid); haskey && conn != nil {
		removeConn(uuid, conn, true)
	}
}

func removeConn(uuid string, expected *tcp2wsSparkle, sendClose bool) {
	removeConnIfWS(uuid, expected, nil, sendClose)
}

func removeConnIfWS(uuid string, expected *tcp2wsSparkle, expectedWS *websocket.Conn, sendClose bool) {
	if expected == nil {
		return
	}

	expected.writeMu.Lock()
	if expectedWS != nil && !expected.currentWSIs(expectedWS) {
		expected.writeMu.Unlock()
		return
	}
	connMapLock.Lock()
	if connMap[uuid] != expected {
		connMapLock.Unlock()
		expected.writeMu.Unlock()
		return
	}
	delete(connMap, uuid)
	connMapLock.Unlock()

	expected.mu.Lock()
	if expected.del {
		expected.mu.Unlock()
		expected.writeMu.Unlock()
		return
	}
	expected.del = true
	wsConn := expected.wsConn
	expected.wsConn = nil
	expected.mu.Unlock()
	expected.cancel()

	if wsConn != nil {
		if sendClose {
			log.Print(uuid, " bye")
			_ = wsConn.WriteMessage(websocket.TextMessage, []byte("tcp2wsSparkleClose"))
		}
		_ = wsConn.Close()
	}
	expected.writeMu.Unlock()

	if expected.udpConn != nil {
		_ = expected.udpConn.Close()
	}
	if expected.tcpConn != nil {
		_ = expected.tcpConn.Close()
	}
}

func dialNewWs(ctx context.Context, uuid string) (*websocket.Conn, error) {
	log.Print("dial ", uuid)
	proxySelected := false
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{RootCAs: nil, InsecureSkipVerify: true},
		Proxy: func(request *http.Request) (*url.URL, error) {
			proxyURL, err := http.ProxyFromEnvironment(request)
			proxySelected = proxyURL != nil
			return proxyURL, err
		},
		NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if proxySelected {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
			}
			return meDialContext(ctx, network, address)
		},
	}
	wsConn, _, err := dialer.DialContext(ctx, wsAddr, nil)
	if err != nil {
		return nil, err
	}
	if err := wsConn.WriteMessage(websocket.TextMessage, []byte(uuid)); err != nil {
		_ = wsConn.Close()
		return nil, err
	}
	return wsConn, nil
}

func preferredIPDialAddress(address, wsURL, preferredIP string) string {
	if preferredIP == "" {
		return address
	}
	parsedURL, err := url.Parse(wsURL)
	if err != nil {
		return address
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || !strings.EqualFold(host, parsedURL.Hostname()) {
		return address
	}
	originPort := parsedURL.Port()
	if originPort == "" {
		if parsedURL.Scheme == "wss" || parsedURL.Scheme == "https" {
			originPort = "443"
		} else {
			originPort = "80"
		}
	}
	if port != originPort {
		return address
	}
	return net.JoinHostPort(strings.Trim(preferredIP, "[]"), port)
}

func startReconnect(conn *tcp2wsSparkle, autoRetry bool, dial wsDialFunc) {
	if conn == nil || conn.isDeleted() || conn.currentWS() != nil {
		return
	}
	conn.reconnectMu.Lock()
	if conn.reconnecting {
		if autoRetry {
			conn.reconnectAuto = true
		} else {
			conn.reconnectPending = true
		}
		conn.reconnectMu.Unlock()
		return
	}
	if time.Now().Before(conn.retryAt) {
		conn.reconnectMu.Unlock()
		return
	}
	conn.reconnecting = true
	conn.reconnectAuto = autoRetry
	conn.reconnectPending = false
	conn.reconnectMu.Unlock()
	go reconnectLoop(conn, dial)
}

func requestReconnect(conn *tcp2wsSparkle, autoRetry bool) {
	startReconnect(conn, autoRetry, dialNewWs)
}

func reconnectLoop(conn *tcp2wsSparkle, dial wsDialFunc) {
	for {
		if conn.isDeleted() {
			finishReconnect(conn)
			return
		}
		wsConn, err := dial(conn.ctx, conn.uuid)
		if err == nil {
			err = installWS(conn, wsConn)
			if err == nil {
				conn.reconnectMu.Lock()
				conn.retryDelay = 0
				conn.retryAt = time.Time{}
				conn.reconnecting = false
				conn.reconnectAuto = false
				conn.reconnectPending = false
				conn.reconnectMu.Unlock()
				go readWs2TcpClient(conn, wsConn)
				return
			}
			if wsConn != nil {
				_ = wsConn.Close()
			}
		}
		if conn.isDeleted() {
			finishReconnect(conn)
			return
		}
		if err != nil {
			log.Print("connect to ws err: ", err)
		} else {
			log.Print("reconnect ws write err: ", err)
		}

		delay := conn.nextReconnectDelay()
		conn.reconnectMu.Lock()
		autoRetry := conn.reconnectAuto
		pending := conn.reconnectPending
		conn.reconnectPending = false
		if !autoRetry && !pending {
			conn.reconnecting = false
			conn.reconnectAuto = false
			conn.reconnectPending = false
		}
		conn.reconnectMu.Unlock()
		if !autoRetry && !pending {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-conn.ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			finishReconnect(conn)
			return
		case <-timer.C:
		}
	}
}

func finishReconnect(conn *tcp2wsSparkle) {
	conn.reconnectMu.Lock()
	conn.reconnecting = false
	conn.reconnectAuto = false
	conn.reconnectPending = false
	conn.reconnectMu.Unlock()
}

func (conn *tcp2wsSparkle) nextReconnectDelay() time.Duration {
	conn.reconnectMu.Lock()
	defer conn.reconnectMu.Unlock()
	delay := conn.retryDelay
	if delay == 0 {
		delay = initialReconnectDelay
	}
	nextDelay := delay * 2
	if nextDelay > maxReconnectDelay {
		nextDelay = maxReconnectDelay
	}
	conn.retryDelay = nextDelay
	conn.retryAt = time.Now().Add(delay)
	return delay
}

func installWS(conn *tcp2wsSparkle, wsConn *websocket.Conn) error {
	conn.writeMu.Lock()
	defer conn.writeMu.Unlock()
	if conn.isDeleted() {
		return errors.New("connection is closed")
	}

	conn.mu.Lock()
	oldWS := conn.wsConn
	conn.wsConn = wsConn
	conn.t = time.Now().Unix()
	conn.mu.Unlock()
	if oldWS != nil && oldWS != wsConn {
		_ = oldWS.Close()
	}

	for index, message := range conn.buf {
		if err := wsConn.WriteMessage(message.messageType, message.data); err != nil {
			conn.buf = conn.buf[index:]
			conn.clearWSIfCurrent(wsConn)
			_ = wsConn.Close()
			return err
		}
	}
	conn.buf = nil
	return nil
}

func writeWS(conn *tcp2wsSparkle, messageType int, data []byte) error {
	conn.writeMu.Lock()
	defer conn.writeMu.Unlock()
	if conn.isDeleted() {
		return errors.New("connection is closed")
	}
	wsConn := conn.currentWS()
	if wsConn == nil {
		return errors.New("websocket is not connected")
	}
	if err := wsConn.WriteMessage(messageType, data); err != nil {
		return err
	}
	conn.updateActivity()
	return nil
}

func sendPayload(conn *tcp2wsSparkle, messageType int, data []byte) bool {
	if len(data) == 0 {
		return true
	}
	message := queuedMessage{messageType: messageType, data: append([]byte(nil), data...)}
	queued := false
	conn.writeMu.Lock()
	if conn.isDeleted() {
		conn.writeMu.Unlock()
		return false
	}
	wsConn := conn.currentWS()
	if wsConn == nil {
		conn.buf = append(conn.buf, message)
		queued = true
	} else if err := wsConn.WriteMessage(message.messageType, message.data); err != nil {
		log.Print(conn.uuid, " ws write err: ", err)
		conn.buf = append(conn.buf, message)
		if !isServer {
			conn.clearWSIfCurrent(wsConn)
		}
		_ = wsConn.Close()
		queued = true
	} else {
		conn.updateActivity()
	}
	conn.writeMu.Unlock()

	if queued && !isServer {
		requestReconnect(conn, !conn.isUdp)
	}
	return !conn.isDeleted()
}

func readTcp2Ws(uuid string) bool {
	defer func() {
		if err := recover(); err != nil {
			log.Print(uuid, " tcp -> ws Boom!\n", err)
		}
	}()

	conn, haskey := getConn(uuid)
	if !haskey || conn == nil {
		return false
	}
	buf := make([]byte, 500000)
	for !conn.isDeleted() {
		var length int
		var err error
		if conn.isUdp {
			var udpAddr *net.UDPAddr
			length, udpAddr, err = conn.udpConn.ReadFromUDP(buf)
			if err == nil {
				conn.setUDPAddr(udpAddr)
			}
		} else {
			length, err = conn.tcpConn.Read(buf)
		}
		if err != nil {
			if !conn.isDeleted() {
				if err.Error() != "EOF" {
					if conn.isUdp {
						log.Print(uuid, " udp read err: ", err)
					} else {
						log.Print(uuid, " tcp read err: ", err)
					}
				}
				removeConn(uuid, conn, true)
			}
			return false
		}
		if length > 0 && !sendPayload(conn, conn.currentMessageType(), buf[:length]) {
			return false
		}
	}
	return false
}

func readWs2Tcp(conn *tcp2wsSparkle, wsConn *websocket.Conn) bool {
	defer func() {
		if err := recover(); err != nil {
			log.Print(conn.uuid, " ws -> tcp Boom!\n", err)
		}
	}()
	if conn == nil || wsConn == nil {
		return false
	}

	for !conn.isDeleted() && conn.currentWSIs(wsConn) {
		messageType, data, err := wsConn.ReadMessage()
		if err != nil || messageType == -1 {
			_ = wsConn.Close()
			if conn.currentWSIs(wsConn) {
				log.Print(conn.uuid, " ws read err: ", err)
				return true
			}
			return false
		}
		if !conn.currentWSIs(wsConn) {
			return false
		}
		if len(data) == 0 {
			continue
		}
		conn.updateActivity()
		if messageType == websocket.TextMessage {
			message := string(data)
			if message == "tcp2wsSparkle" {
				log.Print(conn.uuid, " 咩")
				continue
			}
			if message == "tcp2wsSparkleClose" {
				log.Print(conn.uuid, " say bye")
				removeConnIfWS(conn.uuid, conn, wsConn, false)
				return false
			}
		}
		conn.setMessageType(messageType)
		var writeErr error
		if conn.isUdp {
			if isServer {
				_, writeErr = conn.udpConn.Write(data)
			} else if udpAddr := conn.currentUDPAddr(); udpAddr != nil {
				_, writeErr = conn.udpConn.WriteToUDP(data, udpAddr)
			} else {
				continue
			}
		} else {
			_, writeErr = conn.tcpConn.Write(data)
		}
		if writeErr != nil {
			log.Print(conn.uuid, " backend write err: ", writeErr)
			removeConnIfWS(conn.uuid, conn, wsConn, true)
			return false
		}
	}
	return false
}

func readWs2TcpClient(conn *tcp2wsSparkle, wsConn *websocket.Conn) {
	if readWs2Tcp(conn, wsConn) && conn.clearWSIfCurrent(wsConn) {
		log.Print(conn.uuid, " ws Boom!")
		if !conn.isUdp {
			requestReconnect(conn, true)
		}
	}
}

// 自定义的Dial连接器，自定义域名解析
func meDial(network, address string) (net.Conn, error) {
	return net.DialTimeout(network, preferredIPDialAddress(address, wsAddr, wsAddrIp), 5*time.Second)
}

func meDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	address = preferredIPDialAddress(address, wsAddr, wsAddrIp)
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
}

// 服务端 是tcp还是udp连接是客户端发过来的
func runServer(wsConn *websocket.Conn) {
	defer func() {
		err := recover()
		if err != nil {
			log.Print("server Boom!\n", err)
		}
	}()

	var isUdp bool
	var uuid string
	// read uuid to get from connMap
	t, buf, err := wsConn.ReadMessage()
	if err != nil || t == -1 || len(buf) == 0 {
		log.Print("ws uuid read err: ", err)
		_ = wsConn.Close()
		return
	}
	if t == websocket.TextMessage {
		uuid = string(buf)
		if uuid == "" {
			log.Print("ws uuid read empty")
			return
		}
		// U 开头的uuid为udp连接
		isUdp = strings.HasPrefix(uuid, "U")
		if conn, haskey := getConn(uuid); haskey {
			if err := installWS(conn, wsConn); err != nil {
				log.Print("replace ws conn err: ", err)
				_ = wsConn.Close()
				return
			}
			log.Print("uuid finded ", uuid)
			go readWs2Tcp(conn, wsConn)
			return
		}
	}
	if t != websocket.TextMessage || uuid == "" {
		log.Print("ws uuid message invalid")
		_ = wsConn.Close()
		return
	}

	// uuid没有找到 新连接
	if isUdp {
		// call new udp
		log.Print("new udp for ", uuid)
		udpAddr, err := net.ResolveUDPAddr("udp4", tcpAddr)
		if err != nil {
			log.Print("resolve udp addr err: ", err)
			return
		}
		udpConn, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			log.Print("connect to udp err: ", err)
			_ = wsConn.WriteMessage(websocket.TextMessage, []byte("tcp2wsSparkleClose"))
			_ = wsConn.Close()
			return
		}

		// save
		conn := newTcp2wsSparkle(true, udpConn, nil, wsConn, uuid)
		setConn(uuid, conn)

		go readTcp2Ws(uuid)
	} else {
		// call new tcp
		log.Print("new tcp for ", uuid)
		tcpConn, err := net.Dial("tcp", tcpAddr)
		if err != nil {
			log.Print("connect to tcp err: ", err)
			_ = wsConn.WriteMessage(websocket.TextMessage, []byte("tcp2wsSparkleClose"))
			_ = wsConn.Close()
			return
		}

		// save
		conn := newTcp2wsSparkle(false, nil, tcpConn, wsConn, uuid)
		setConn(uuid, conn)

		go readTcp2Ws(uuid)
	}

	conn, _ := getConn(uuid)
	go readWs2Tcp(conn, wsConn)
}

// tcp客户端
func runClient(tcpConn net.Conn, uuid string) {
	defer func() {
		err := recover()
		if err != nil {
			log.Print("client Boom!\n", err)
		}
	}()

	if tcpConn == nil {
		return
	}
	conn := newTcp2wsSparkle(false, nil, tcpConn, nil, uuid)
	setConn(uuid, conn)
	go readTcp2Ws(uuid)
	requestReconnect(conn, true)
}

// udp客户端
func runClientUdp(listenHostPort string) {
	defer func() {
		err := recover()
		if err != nil {
			log.Print("udp client Boom!\n", err)
		}
	}()
	uuid := "U" + uuid.New().String()[32:]
	for {
		log.Print("Create UDP Listen: ", listenHostPort)
		// 开udp监听
		udpAddr, err := net.ResolveUDPAddr("udp4", listenHostPort)
		if err != nil {
			log.Print("UDP Addr Resolve Error: ", err)
			return
		}
		udpConn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			log.Print("UDP Listen Start Error: ", err)
			return
		}

		// save
		setConn(uuid, newTcp2wsSparkle(true, udpConn, nil, nil, uuid))

		// 收到内容后会开ws连接并拿到UDPAddr 阻塞
		readTcp2Ws(uuid)
	}

}

// 响应ws请求
func wsHandler(w http.ResponseWriter, r *http.Request) {
	forwarded := r.Header.Get("X-Forwarded-For")
	// 不是ws的请求返回index.html 假装是一个静态服务器
	if r.Header.Get("Upgrade") != "websocket" {
		if forwarded == "" {
			log.Print("not ws: ", r.RemoteAddr)
		} else {
			log.Print("not ws: ", forwarded)
		}
		_, err := os.Stat("index.html")
		if err == nil {
			http.ServeFile(w, r, "index.html")
		}
		return
	} else {
		if forwarded == "" {
			log.Print("new ws conn: ", r.RemoteAddr)
		} else {
			log.Print("new ws conn: ", forwarded)
		}
	}

	// ws协议握手
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("ws upgrade err: ", err)
		return
	}

	// 新线程hold住这条连接
	go runServer(conn)
}

// 响应tcp
func tcpHandler(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Print("tcp accept err: ", err)
			return
		}

		log.Print("new tcp conn: ")

		// 新线程hold住这条连接
		go runClient(conn, uuid.New().String()[31:])
	}
}

// 启动ws服务
func startWsServer(listenPort string, isSsl bool, sslCrt string, sslKey string) {
	var err error = nil
	if isSsl {
		fmt.Println("use ssl cert: " + sslCrt + " " + sslKey)
		err = http.ListenAndServeTLS(listenPort, sslCrt, sslKey, nil)
	} else {
		err = http.ListenAndServe(listenPort, nil)
	}
	if err != nil {
		log.Fatal("tcp2ws Server Start Error: ", err)
	}
}

// 又造轮子了 发现给v4的ip加个框也能连诶
func tcping(hostname, port string) int64 {
	st := time.Now().UnixNano()
	c, err := net.DialTimeout("tcp", "["+hostname+"]"+port, 5*time.Second)
	if err != nil {
		return -1
	}
	c.Close()
	return (time.Now().UnixNano() - st) / 1e6
}

// 优选ip
func dnsPreferIp(hostname string) (string, uint32) {
	// 由正则驱动的hosts解析器 此解析器拥有超咩力
	hostsFile := "/etc/hosts"
	if runtime.GOOS == "windows" {
		hostsFile = os.Getenv("SystemRoot") + `\System32\drivers\etc\hosts`
	}
	hosts, err := ioutil.ReadFile(hostsFile)
	if err == nil {
		re := regexp.MustCompile(`(?m)^([0-9.]+).*[ \t](` + hostname + `$|` + hostname + ` .*)`)
		matches := re.FindAllStringSubmatch(string(hosts), -1)
		if len(matches) > 0 {
			log.Print("Use System hosts: ", matches[0][1], " ", hostname)
			return matches[0][1], 0
		}
	} else {
		log.Print(`Read System hosts "`, hostsFile, `" error: `, err)
	}

	// 从dns获取
	log.Print("nslookup " + hostname)

	tc := dns.Client{Net: "tcp", Timeout: 10 * time.Second}
	uc := dns.Client{Net: "udp", Timeout: 10 * time.Second}
	m := dns.Msg{}
	m.SetQuestion(hostname+".", dns.TypeA)

	// 获取系统配置的dns 如果有就用它解析域名 windows咩咩不用不知道怎么写所以不支持
	// 由正则驱动的resolv.conf解析器 此解析器拥有超咩力
	systemDns := "127.0.0.1"
	if runtime.GOOS != "windows" {
		resolv, err := ioutil.ReadFile("/etc/resolv.conf")
		if err == nil {
			re := regexp.MustCompile(`(?m)^nameserver[ \t]+(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}).*`)
			matches := re.FindAllStringSubmatch(string(resolv), -1)
			if len(matches) > 0 {
				systemDns = matches[0][1]
			}
		} else {
			log.Print(`Read System resolv.conf "/etc/resolv.conf" error: `, err)
		}
	} else {
		// ipconfig /all 会输出系统设置的dns 依然由正则驱动
		cmd := exec.Command("ipconfig", "/all")
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Print("Run ipconfig error: ", err)
		} else {
			// 从DNS匹配到下一个项目
			reBlock := regexp.MustCompile(`(?s)DNS\s+[^:]+:\s*([\s\S]+?)(?:\r?\n\s{1,8}[^\s\.]|$)`)
			reIPv4 := regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
			blocks := reBlock.FindAllStringSubmatch(string(output), -1)
			for _, match := range blocks {
				if len(match) < 2 {
					continue
				}
				// 只要IPv4
				ipv4s := reIPv4.FindAllString(match[1], -1)
				if len(ipv4s) > 0 {
					for _, ip := range ipv4s {
						if ip != "0.0.0.0" {
							systemDns = ip
							break
						}
					}
				}
			}
		}
	}
	log.Print("System DNS ", systemDns)
	r, _, err := uc.Exchange(&m, systemDns+":53")
	if err != nil {
		log.Print("Use System DNS Fail: ", err)
		r, _, err = tc.Exchange(&m, "208.67.222.222:53")
		if err != nil {
			log.Print("OpenDNS Fail: ", err)
			return "", 0
		}
	}
	if len(r.Answer) == 0 {
		log.Print("Could not found NS records")
		return "", 0
	}

	ip := ""
	var ttl uint32 = 60
	var lastPing int64 = 5000
	for _, ans := range r.Answer {
		if a, ok := ans.(*dns.A); ok {
			nowPing := tcping(a.A.String(), wsAddrPort)
			log.Print("tcping "+a.A.String()+" ", nowPing, "ms")
			if nowPing != -1 && nowPing < lastPing {
				ip = a.A.String()
				ttl = ans.Header().Ttl
				lastPing = nowPing
			}
		}
	}
	log.Print("Prefer IP " + ip + " for " + hostname)
	return ip, ttl
}

// 根据dns ttl自动更新ip
func dnsPreferIpWithTtl(hostname string, ttl uint32) {
	for {
		log.Println("DNS TTL: ", ttl, "s")
		time.Sleep(time.Duration(ttl) * time.Second)
		log.Println("Update IP for " + hostname)
		ip, ttlNow := dnsPreferIp(hostname)
		if ip != "" {
			wsAddrIp = ip
			ttl = ttlNow
		} else {
			log.Println("DNS Fail, Use Last IP: " + wsAddrIp)
		}
	}
}

func main() {
	arg_num := len(os.Args)
	if arg_num < 3 {
		fmt.Println("TCP over WebSocket (tcp2ws) with UDP support v11.3\nhttps://github.com/zanjie1999/tcp-over-websocket")
		fmt.Println("Client: ws://tcp2wsUrl localPort\nServer: ip:port tcp2wsPort\nUse wss: ip:port tcp2wsPort server.crt server.key")
		fmt.Println("Make ssl cert:\nopenssl genrsa -out server.key 2048\nopenssl ecparam -genkey -name secp384r1 -out server.key\nopenssl req -new -x509 -sha256 -key server.key -out server.crt -days 36500")
		os.Exit(0)
	}
	serverUrl := os.Args[1]
	listenPort := os.Args[2]
	isSsl := false
	if arg_num == 4 {
		isSsl = os.Args[3] == "wss" || os.Args[3] == "https" || os.Args[3] == "ssl"
	}
	sslCrt := "server.crt"
	sslKey := "server.key"
	if arg_num == 5 {
		isSsl = true
		sslCrt = os.Args[3]
		sslKey = os.Args[4]
	}

	// 第一个参数是ws
	match, _ := regexp.MatchString(`^(ws|wss|http|https)://.*`, serverUrl)
	isServer = !match
	if isServer {
		// 服务端
		match, _ := regexp.MatchString(`^\d+$`, serverUrl)
		if match {
			// 只有端口号默认127.0.0.1
			tcpAddr = "127.0.0.1:" + serverUrl
		} else {
			tcpAddr = serverUrl
		}
		// ws server
		http.HandleFunc("/", wsHandler)
		match, _ = regexp.MatchString(`^\d+$`, listenPort)
		listenHostPort := listenPort
		if match {
			// 如果没指定监听ip那就全部监听 省掉不必要的防火墙
			listenHostPort = "0.0.0.0:" + listenPort
		}
		go startWsServer(listenHostPort, isSsl, sslCrt, sslKey)
		if isSsl {
			log.Print("Server Started wss://" + listenHostPort + " -> " + tcpAddr)
			fmt.Print("Proxy with Nginx:\nlocation /" + uuid.New().String()[24:] + "/ {\nproxy_pass https://")
		} else {
			log.Print("Server Started ws://" + listenHostPort + " -> " + tcpAddr)
			fmt.Print("Proxy with Nginx:\nlocation /" + uuid.New().String()[24:] + "/ {\nproxy_pass http://")
		}
		if match {
			fmt.Print("127.0.0.1:" + listenPort)
		} else {
			fmt.Print(listenPort)
		}
		fmt.Println("/;\nproxy_read_timeout 3600;\nproxy_http_version 1.1;\nproxy_set_header Upgrade $http_upgrade;\nproxy_set_header Connection \"Upgrade\";\nproxy_set_header X-Forwarded-For $remote_addr;\naccess_log off;\n}")
	} else {
		// 客户端
		if serverUrl[:5] == "https" {
			wsAddr = "wss" + serverUrl[5:]
		} else if serverUrl[:4] == "http" {
			wsAddr = "ws" + serverUrl[4:]
		} else {
			wsAddr = serverUrl
		}
		match, _ = regexp.MatchString(`^\d+$`, listenPort)
		listenHostPort := listenPort
		if match {
			// 如果没指定监听ip那就全部监听 省掉不必要的防火墙
			listenHostPort = "0.0.0.0:" + listenPort
		}
		l, err := net.Listen("tcp", listenHostPort)
		if err != nil {
			log.Fatal("tcp2ws Client Start Error: ", err)
		}
		// 将ws服务端域名对应的ip缓存起来，避免多次请求dns或dns爆炸导致无法连接
		u, err := url.Parse(wsAddr)
		if err != nil {
			log.Fatal("tcp2ws Client Start Error: ", err)
		}
		// 确定端口号，下面域名tcping要用
		if u.Port() != "" {
			wsAddrPort = ":" + u.Port()
		} else if wsAddr[:3] == "wss" {
			wsAddrPort = ":443"
		} else {
			wsAddrPort = ":80"
		}
		if u.Host[0] == '[' {
			// ipv6
			wsAddrIp = "[" + u.Hostname() + "]"
			log.Print("tcping "+u.Hostname()+" ", tcping(u.Hostname(), wsAddrPort), "ms")
		} else if match, _ = regexp.MatchString(`^\d+.\d+.\d+.\d+$`, u.Hostname()); match {
			// ipv4
			wsAddrIp = u.Hostname()
			log.Print("tcping "+wsAddrIp+" ", tcping(wsAddrIp, wsAddrPort), "ms")
		} else {
			// 域名，需要解析，ip优选
			var ttl uint32
			wsAddrIp, ttl = dnsPreferIp(u.Hostname())
			if wsAddrIp == "" {
				log.Fatal("tcp2ws Client Start Error: dns resolve error")
			} else if ttl > 0 {
				// 根据dns ttl自动更新ip
				go dnsPreferIpWithTtl(u.Hostname(), ttl)
			}
		}

		go tcpHandler(l)

		// 启动一个udp监听用于udp转发
		go runClientUdp(listenHostPort)

		log.Print("Client Started " + listenHostPort + " -> " + wsAddr)
	}
	for {
		if isServer {
			// 心跳间隔2分钟
			time.Sleep(2 * 60 * time.Second)
			nowTimeCut := time.Now().Unix() - 2*60
			// check ws
			for k, i := range connMapSnapshot() {
				// 如果超过2分钟没有收到消息，才发心跳，避免读写冲突
				if i.lastActivity() < nowTimeCut {
					if i.isUdp {
						// udp不需要心跳 超时就关闭
						log.Print(i.uuid, " udp timeout close")
						deleteConn(k)
					} else if err := writeWS(i, websocket.TextMessage, []byte("tcp2wsSparkle")); err != nil {
						log.Print(i.uuid, " tcp timeout close")
						deleteConn(k)
					}
				}
			}
		} else {
			// 按 ctrl + c 退出，会阻塞
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt, os.Kill)
			<-c
			fmt.Println()
			log.Print("quit...")
			for k := range connMapSnapshot() {
				deleteConn(k)
			}
			os.Exit(0)
		}
	}
}
