// Tcp over WebSocket (tcp2ws)
// 基于ws的内网穿透工具
// Sparkle 20210430
// v9.0

package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/miekg/dns"
)

type tcp2wsSparkle struct {
	isUdp   bool
	udpConn *net.UDPConn
	udpAddr *net.UDPAddr
	tcpConn net.Conn
	wsConn  *websocket.Conn
	uuid    string
	del     bool
	buf     [][]byte
	t       int64
}

var (
	tcpAddr    string
	wsAddr     string
	wsAddrIp   string
	wsAddrPort     = ""
	msgType    int = websocket.BinaryMessage
	isServer   bool
	connMap    map[string]*tcp2wsSparkle = make(map[string]*tcp2wsSparkle)
	// go的map不是线程安全的 读写冲突就会直接exit
	connMapLock *sync.RWMutex = new(sync.RWMutex)
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

func deleteConn(uuid string) {
	if conn, haskey := getConn(uuid); haskey && conn != nil && !conn.del {
		connMapLock.Lock()
		defer connMapLock.Unlock()
		conn.del = true
		if conn.udpConn != nil {
			conn.udpConn.Close()
		}
		if conn.tcpConn != nil {
			conn.tcpConn.Close()
		}
		if conn.wsConn != nil {
			log.Print(uuid, " bye")
			conn.wsConn.WriteMessage(websocket.TextMessage, []byte("tcp2wsSparkleClose"))
			conn.wsConn.Close()
		}
		delete(connMap, uuid)
	}
}

func dialNewWs(uuid string) bool {
	log.Print("dial ", uuid)
	// call ws
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: nil, InsecureSkipVerify: true}, Proxy: http.ProxyFromEnvironment, NetDial: meDial}
	wsConn, _, err := dialer.Dial(wsAddr, nil)
	if err != nil {
		log.Print("connect to ws err: ", err)
		return false
	}
	// send uuid
	if err := wsConn.WriteMessage(websocket.TextMessage, []byte(uuid)); err != nil {
		log.Print("udp send ws uuid err: ", err)
		wsConn.Close()
		return false
	}
	// update
	if conn, haskey := getConn(uuid); haskey {
		if conn.wsConn != nil {
			conn.wsConn.Close()
		}
		conn.wsConn = wsConn
		conn.t = time.Now().Unix()
		writeErrorBuf2Ws(conn)
	}
	return true
}

// 将tcp或udp的数据转发到ws
func readTcp2Ws(uuid string) bool {
	defer func() {
		err := recover()
		if err != nil {
			log.Print(uuid, " tcp -> ws Boom!\n", err)
			readTcp2Ws(uuid)
		}
	}()

	conn, haskey := getConn(uuid)
	if !haskey {
		return false
	}
	buf := make([]byte, 500000)
	tcpConn := conn.tcpConn
	udpConn := conn.udpConn
	isUdp := conn.isUdp
	for {
		if conn.del || !isUdp && tcpConn == nil || isUdp && udpConn == nil {
			return false
		}
		var length int
		var err error
		if isUdp {
			length, conn.udpAddr, err = udpConn.ReadFromUDP(buf)
			// 客户端udp先收到内容再创建ws连接 服务端不可能进入这里
			if conn.wsConn == nil {
				if !dialNewWs(uuid) {
					// ws连接失败 存起来 下次重试
					if conn.buf == nil {
						conn.buf = [][]byte{buf[:length]}
					} else {
						conn.buf = append(conn.buf, buf[:length])
					}
					continue
				}
				go readWs2TcpClient(uuid, true)
			}
		} else {
			length, err = tcpConn.Read(buf)
		}
		if err != nil {
			if conn, haskey := getConn(uuid); haskey && !conn.del {
				// tcp中断 关闭所有连接 关过的就不用关了
				if err.Error() != "EOF" {
					if isUdp {
						log.Print(uuid, " udp read err: ", err)
					} else {
						log.Print(uuid, " tcp read err: ", err)
					}
				}
				deleteConn(uuid)
				return false
			}
			return false
		}
		// log.Print(uuid, " ws send: ", length)
		if length > 0 {
			// 因为tcpConn.Read会阻塞 所以要从connMap中获取最新的wsConn
			conn, haskey := getConn(uuid)
			if !haskey || conn.del {
				return false
			}
			wsConn := conn.wsConn
			if wsConn == nil {
				return false
			}
			conn.t = time.Now().Unix()
			if err = wsConn.WriteMessage(msgType, buf[:length]); err != nil {
				log.Print(uuid, " ws write err: ", err)
				// tcpConn.Close()
				wsConn.Close()
				// save send error buf
				if conn.buf == nil {
					conn.buf = [][]byte{buf[:length]}
				} else {
					conn.buf = append(conn.buf, buf[:length])
				}
				// 此处无需中断 等着新的wsConn 或是被 断开连接 / 回收 即可
			}
			// if !isServer {
			// 	log.Print(uuid, " send: ", length)
			// }
		}
	}
}

// 将ws的数据转发到tcp或udp
func readWs2Tcp(uuid string) bool {
	defer func() {
		err := recover()
		if err != nil {
			log.Print(uuid, " ws -> tcp Boom!\n", err)
			readWs2Tcp(uuid)
		}
	}()

	conn, haskey := getConn(uuid)
	if !haskey {
		return false
	}
	wsConn := conn.wsConn
	tcpConn := conn.tcpConn
	udpConn := conn.udpConn
	isUdp := conn.isUdp
	for {
		if conn.del || !isUdp && tcpConn == nil || isUdp && udpConn == nil || wsConn == nil {
			return false
		}
		t, buf, err := wsConn.ReadMessage()
		if err != nil || t == -1 {
			wsConn.Close()
			if conn, haskey := getConn(uuid); haskey && !conn.del {
				// 外部干涉导致中断 重连ws
				log.Print(uuid, " ws read err: ", err)
				return true
			}
			return false
		}
		// log.Print(uuid, " ws recv: ", len(buf))
		if len(buf) > 0 {
			conn.t = time.Now().Unix()
			if t == websocket.TextMessage {
				msg := string(buf)
				if msg == "tcp2wsSparkle" {
					log.Print(uuid, " 咩")
					continue
				} else if msg == "tcp2wsSparkleClose" {
					log.Print(uuid, " say bye")
					connMapLock.Lock()
					defer connMapLock.Unlock()
					wsConn.Close()
					if isUdp {
						udpConn.Close()
					} else {
						tcpConn.Close()
					}
					delete(connMap, uuid)
					return false
				}
			}
			msgType = t
			if isUdp {
				if isServer {
					if _, err = udpConn.Write(buf); err != nil {
						log.Print(uuid, " udp write err: ", err)
						deleteConn(uuid)
						return false
					}
				} else {
					// 客户端作为udp服务端回复需要udp客户端发送数据时提供的udpAddr
					if _, err = udpConn.WriteToUDP(buf, conn.udpAddr); err != nil {
						log.Print(uuid, " udp write err: ", err)
						deleteConn(uuid)
						return false
					}
				}
			} else {
				if _, err = tcpConn.Write(buf); err != nil {
					log.Print(uuid, " tcp write err: ", err)
					deleteConn(uuid)
					return false
				}
			}
		}
	}
}

// 多了一个被动断开后自动重连的功能
func readWs2TcpClient(uuid string, isUdp bool) {
	if readWs2Tcp(uuid) {
		log.Print(uuid, " ws Boom!")
		// error return  re call ws
		if !isUdp {
			runClient(nil, uuid)
		} else {
			// 删除wsConn 下次收到udp数据时会重新建立ws连接
			conn, haskey := getConn(uuid)
			if haskey {
				conn.wsConn = nil
			}
		}
	}
}

// 将没写成的内容写到ws
func writeErrorBuf2Ws(conn *tcp2wsSparkle) {
	if conn != nil {
		for i := 0; i < len(conn.buf); i++ {
			conn.wsConn.WriteMessage(websocket.BinaryMessage, conn.buf[i])
		}
		conn.buf = nil
	}
}

// 自定义的Dial连接器，自定义域名解析
func meDial(network, address string) (net.Conn, error) {
	// return net.DialTimeout(network, address, 5 * time.Second)
	return net.DialTimeout(network, wsAddrIp+wsAddrPort, 5*time.Second)
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
	var udpConn *net.UDPConn
	var tcpConn net.Conn
	var uuid string
	// read uuid to get from connMap
	t, buf, err := wsConn.ReadMessage()
	if err != nil || t == -1 || len(buf) == 0 {
		log.Print("ws uuid read err: ", err)
		wsConn.Close()
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
			// get
			udpConn = conn.udpConn
			tcpConn = conn.tcpConn
			conn.wsConn.Close()
			conn.wsConn = wsConn
			writeErrorBuf2Ws(conn)
		}
	}

	// uuid没有找到 新连接
	if isUdp && udpConn == nil {
		// call new udp
		log.Print("new udp for ", uuid)
		udpAddr, err := net.ResolveUDPAddr("udp4", tcpAddr)
		if err != nil {
			log.Print("resolve udp addr err: ", err)
			return
		}
		udpConn, err = net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			log.Print("connect to udp err: ", err)
			return
		}

		// save
		setConn(uuid, &tcp2wsSparkle{true, udpConn, nil, nil, wsConn, uuid, false, nil, time.Now().Unix()})

		go readTcp2Ws(uuid)
	} else if !isUdp && tcpConn == nil {
		// call new tcp
		log.Print("new tcp for ", uuid)
		tcpConn, err = net.Dial("tcp", tcpAddr)
		if err != nil {
			log.Print("connect to tcp err: ", err)
			return
		}

		// save
		setConn(uuid, &tcp2wsSparkle{false, nil, nil, tcpConn, wsConn, uuid, false, nil, time.Now().Unix()})

		go readTcp2Ws(uuid)
	} else {
		log.Print("uuid finded ", uuid)
	}

	go readWs2Tcp(uuid)
}

// tcp客户端
func runClient(tcpConn net.Conn, uuid string) {
	defer func() {
		err := recover()
		if err != nil {
			log.Print("client Boom!\n", err)
		}
	}()

	// conn is close?
	if tcpConn == nil {
		if conn, haskey := getConn(uuid); haskey {
			if conn.del {
				return
			}
		} else {
			return
		}
	}
	// save conn
	if tcpConn != nil {
		setConn(uuid, &tcp2wsSparkle{false, nil, nil, tcpConn, nil, uuid, false, nil, time.Now().Unix()})
	}
	dialNewWs(uuid)

	go readWs2TcpClient(uuid, false)
	if tcpConn != nil {
		// 不是重连
		go readTcp2Ws(uuid)
	}
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
		setConn(uuid, &tcp2wsSparkle{true, udpConn, nil, nil, nil, uuid, false, nil, time.Now().Unix()})

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
	log.Print("nslookup " + hostname)

	tc := dns.Client{Net: "tcp", Timeout: 10 * time.Second}
	uc := dns.Client{Net: "udp", Timeout: 10 * time.Second}
	m := dns.Msg{}
	m.SetQuestion(hostname+".", dns.TypeA)
	r, _, err := uc.Exchange(&m, "127.0.0.1:53")
	if err != nil {
		log.Print("Local DNS Fail: ", err)
		r, _, err = tc.Exchange(&m, "208.67.222.222:5353")
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
	log.Println("DNS TTL: ", ttl, "s")
	ip := ""
	for {
		time.Sleep(time.Duration(ttl) * time.Second)
		log.Println("Update IP for " + hostname)
		ip, ttl = dnsPreferIp(hostname)
		if ip != "" {
			wsAddrIp = ip
		} else {
			log.Println("DNS Fail, Use Last IP: " + wsAddrIp)
		}
	}
}

func main() {
	arg_num := len(os.Args)
	if arg_num < 3 {
		fmt.Println("TCP over WebSocket (tcp2ws) with UDP support v9.0\nhttps://github.com/zanjie1999/tcp-over-websocket")
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
			wsAddrIp, ttl := dnsPreferIp(u.Hostname())
			if wsAddrIp == "" {
				log.Fatal("tcp2ws Client Start Error: dns resolve error")
			} else {
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
			for k, i := range connMap {
				// 如果超过2分钟没有收到消息，才发心跳，避免读写冲突
				if i.t < nowTimeCut {
					if i.isUdp {
						// udp不需要心跳 超时就关闭
						log.Print(i.uuid, " udp timeout close")
						deleteConn(k)
					} else if err := i.wsConn.WriteMessage(websocket.TextMessage, []byte("tcp2wsSparkle")); err != nil {
						log.Print(i.uuid, " tcp timeout close")
						i.wsConn.Close()
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
			for k, _ := range connMap {
				deleteConn(k)
			}
			os.Exit(0)
		}
	}
}
