// Tcp over WebSocket (tcp2ws)
// 基于ws的内网穿透工具
// Sparkle 20210430
// v6.0

package main

import (
	"github.com/gorilla/websocket"
	"github.com/google/uuid"
	"log"
	"net"
	"net/http"
	"os"
	"fmt"
	"regexp"
	"time"
	"os/signal"
	"sync"
	"crypto/tls"
)

type tcp2wsSparkle struct {
	tcpConn net.Conn
	wsConn *websocket.Conn
	uuid string
	del bool
	buf [][]byte
 }

var (
	tcp_addr string
	ws_addr string
	msg_type int = websocket.BinaryMessage
	isServer bool
	connMap map[string]*tcp2wsSparkle = make(map[string]*tcp2wsSparkle)
	// go的map不是线程安全的 读写冲突就会直接exit
	connMapLock *sync.RWMutex = new(sync.RWMutex)
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool{ return true },
}

func getConn(uuid string) (*tcp2wsSparkle, bool) {
	connMapLock.RLock()
	defer connMapLock.RUnlock()
	conn, haskey := connMap[uuid];
	return conn, haskey
}

func setConn(uuid string, conn *tcp2wsSparkle) {
	connMapLock.Lock()
	defer connMapLock.Unlock()
	connMap[uuid] = conn
}

func deleteConn(uuid string) {
	if conn, haskey := getConn(uuid); haskey && conn != nil && !conn.del{
		conn.del = true
		if conn.tcpConn != nil {
			conn.tcpConn.Close()
		}
		if conn.wsConn != nil {
			log.Print(uuid, " bye")
			conn.wsConn.WriteMessage(websocket.TextMessage, []byte("tcp2wsSparkleClose"))
			conn.wsConn.Close()
		}
		connMapLock.Lock()
		defer connMapLock.Unlock()
		delete(connMap, uuid)
	}
	// panic("炸一下试试")
}

func ReadTcp2Ws(uuid string) (bool) {
	defer func() {
		err := recover()
		if err != nil {
			log.Print(uuid, " tcp -> ws Boom!\n", err)
			ReadTcp2Ws(uuid)
		}
	}()

	conn, haskey := getConn(uuid);
	if !haskey {
		return false
	}
	buf := make([]byte, 500000)
	tcpConn := conn.tcpConn
	for {
		if conn.del || tcpConn == nil {
			return false
		}
		length,err := tcpConn.Read(buf)
		if err != nil {
			if conn, haskey := getConn(uuid); haskey && !conn.del {
				// tcp中断 关闭所有连接 关过的就不用关了
				log.Print(uuid, " tcp read err: ", err)
				deleteConn(uuid)
				return false
			}
			return false
		}
		if length > 0 {
			// 因为tcpConn.Read会阻塞 所以要从connMap中获取最新的wsConn
			conn, haskey := getConn(uuid);
			if !haskey || conn.del {
				return false
			}
			wsConn := conn.wsConn
			if wsConn == nil {
				return false
			}
			if err = wsConn.WriteMessage(msg_type, buf[:length]);err != nil{
				log.Print(uuid, " ws write err: ", err)
				// tcpConn.Close()
				wsConn.Close()
				// save send error buf
				if conn.buf == nil{
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

func ReadWs2Tcp(uuid string) (bool) {
	defer func() {
		err := recover()
		if err != nil {
			log.Print(uuid, " ws -> tcp Boom!\n", err)
			ReadWs2Tcp(uuid)
		}
	}()

	conn, haskey := getConn(uuid);
	if !haskey {
		return false
	}
	wsConn := conn.wsConn
	tcpConn := conn.tcpConn
	for {
		if conn.del || tcpConn == nil || wsConn == nil {
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
		if len(buf) > 0 {
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
					tcpConn.Close()
					delete(connMap, uuid)
					return false
				}
			}
			msg_type = t
			if _, err = tcpConn.Write(buf);err != nil{
				log.Print(uuid, " tcp write err: ", err)
				deleteConn(uuid)
				return false
			}
			// if !isServer {
			// 	log.Print(uuid, " recv: ", len(buf))	
			// }
		}
	}
}

func ReadWs2TcpClient(uuid string) {
	if ReadWs2Tcp(uuid) {
		// error return  re call ws
		RunClient(nil, uuid)
	}
}

func writeErrorBuf2Ws(uuid string)  {
	if conn, haskey := getConn(uuid); haskey && conn.buf != nil {
		for i := 0; i < len(conn.buf); i++ {
			conn.wsConn.WriteMessage(websocket.BinaryMessage, conn.buf[i])
		}
		conn.buf = nil
	}
}

func RunServer(wsConn *websocket.Conn) {
	defer func() {
		err := recover()
		if err != nil {
			log.Print("server Boom!\n", err)
		}
	}()
	
	log.Print("new ws conn: ", wsConn.RemoteAddr().String())

	var tcpConn net.Conn
	var uuid string
	// read uuid to get from connMap
	t, buf, err := wsConn.ReadMessage()
	if err != nil || t == -1 {
		log.Print(" ws uuid read err: ", err)
		wsConn.Close()
		return
	}
	if len(buf) > 0 {
		if t == websocket.TextMessage {
			uuid = string(buf)
			// get
			if conn, haskey := getConn(uuid); haskey {
				tcpConn = conn.tcpConn
				conn.wsConn.Close()
				conn.wsConn = wsConn
				writeErrorBuf2Ws(uuid)
			}
		}
	}

	if tcpConn == nil {
		log.Print("new tcp for ", uuid)
		// call tcp
		tcpConn, err = net.Dial("tcp", tcp_addr)
		if(err != nil) {
			log.Print("connect to tcp err: ", err)
			return
		}
		if uuid != "" {
			// save
			setConn(uuid, &tcp2wsSparkle {tcpConn, wsConn, uuid, false, nil})
		}

		go ReadTcp2Ws(uuid)
	} else {
		log.Print("uuid finded ", uuid)
	}
	
	go ReadWs2Tcp(uuid)
}

func RunClient(tcpConn net.Conn, uuid string) {
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
	log.Print(uuid, " dial")
	// call ws
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: nil, InsecureSkipVerify: true}}
	wsConn, _, err := dialer.Dial(ws_addr, nil)
	if err != nil {
		log.Print("connect to ws err: ", err)
		if tcpConn != nil {
			tcpConn.Close()
		}
		return
	}
	// send uuid
	if err := wsConn.WriteMessage(websocket.TextMessage, []byte(uuid));err != nil{
		log.Print("send ws uuid err: ", err)
		if tcpConn != nil {
			tcpConn.Close()
		}
		wsConn.Close()
		return
	}
	
	// save conn
	if tcpConn != nil {
		// save
		setConn(uuid, &tcp2wsSparkle {tcpConn, wsConn, uuid, false, nil})
	} else {
		// update
		if conn, haskey := getConn(uuid); haskey {
			conn.wsConn.Close()
			conn.wsConn = wsConn
			writeErrorBuf2Ws(uuid)
		}
	}

	go ReadWs2TcpClient(uuid)
	if tcpConn != nil {
		go ReadTcp2Ws(uuid)
	}
}



// 响应ws请求
func wsHandler(w http.ResponseWriter , r *http.Request){
	// 不是ws的请求返回index.html 假装是一个静态服务器
	if r.Header.Get("Upgrade") != "websocket" {
		forwarded := r.Header.Get("X-FORWARDED-FOR")
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
	} 

	// ws协议握手
	conn, err := upgrader.Upgrade(w,r,nil)
	if err != nil{
		log.Print("ws upgrade err: ", err)
		fmt.Fprintf(w, "1111")
		return 
	}

	// 新线程hold住这条连接
	go RunServer(conn) 
}

// 响应tcp
func tcpHandler(listener net.Listener){
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Print("tcp accept err: ", err)
			return
		}

		log.Print("new tcp conn: ")

		// 新线程hold住这条连接
		go RunClient(conn, uuid.New().String()[32:])
	}
}


func main() {
	arg_num:=len(os.Args)
	if arg_num < 2 {
		fmt.Println("Client: ws://tcp2wsUrl localPort\nServer: ip:port tcp2wsPort\nuse wss: ip:port tcp2wsPort server.crt server.key")
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
			tcp_addr = "127.0.0.1:" + serverUrl
		} else {
			tcp_addr = serverUrl
		}
		// ws server
		http.HandleFunc("/", wsHandler)
		if isSsl {
			fmt.Println("use ssl cert: " + sslCrt + " " + sslKey)
			go http.ListenAndServeTLS("0.0.0.0:"+listenPort, sslCrt, sslKey, nil)
			fmt.Println("Proxy with Nginx:\nlocation /ws/ {\nproxy_pass https://127.0.0.1:" + listenPort + "/;\nproxy_read_timeout 3600;\nproxy_http_version 1.1;\nproxy_set_header Upgrade $http_upgrade;\nproxy_set_header Connection \"Upgrade\";\nproxy_set_header X-Forwarded-For $remote_addr;\n}")
			log.Print("Server Started wss://127.0.0.1:" +  listenPort + " -> " + tcp_addr )
		} else {
			go http.ListenAndServe("0.0.0.0:" + listenPort, nil)
			fmt.Println("Proxy with Nginx:\nlocation /ws/ {\nproxy_pass http://127.0.0.1:" + listenPort + "/;\nproxy_read_timeout 3600;\nproxy_http_version 1.1;\nproxy_set_header Upgrade $http_upgrade;\nproxy_set_header Connection \"Upgrade\";\nproxy_set_header X-Forwarded-For $remote_addr;\n}")
			log.Print("Server Started ws://127.0.0.1:" +  listenPort + " -> " + tcp_addr )
		}
	} else {
		// 客户端
		if match, _ := regexp.MatchString("^http://.*", serverUrl); match {
			ws_addr = "ws" + serverUrl[4:]
		} else if match, _ := regexp.MatchString("^https://.*", serverUrl); match {
			ws_addr = "wss" + serverUrl[5:]
		} else {
			ws_addr = serverUrl
		}
		l, err := net.Listen("tcp", "0.0.0.0:" + listenPort)
		if err != nil {
			log.Print("create listen err: ", err)
			os.Exit(1)
		}
		go tcpHandler(l)
		log.Print("Client Started " +  listenPort + " -> " + ws_addr)
	}
	for {
		if isServer {
			time.Sleep(2 * 60 * time.Second)
			// check ws
			for k, i := range connMap {
				if err := i.wsConn.WriteMessage(websocket.TextMessage, []byte("tcp2wsSparkle"));err != nil{
					log.Print(i.uuid, " timeout close")
					i.tcpConn.Close()
					i.wsConn.Close()
					deleteConn(k)
				}
			}
		} else {
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
