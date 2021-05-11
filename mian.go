// Tcp over WebSocket (tcp2ws)
// 基于ws的内网穿透工具
// Sparkle 20210430
// v3.0

package main

import (
	"github.com/gorilla/websocket"
	"log"
	"net"
	"net/http"
	"os"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var (
	tcp_addr string
	ws_addr string
	conn_num int
	msg_type int
	isServer bool
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool{ return true },
}

func ReadTcp2Ws(id string, tcpConn net.Conn, wsConn *websocket.Conn) {
	buf := make([]byte, 16392)
	for {
		length,err := tcpConn.Read(buf)
		if err != nil {
			log.Print(id, " tcp read err: ", err)
			wsConn.Close()
			tcpConn.Close()
			return
		}
		if length > 0 {
			if err = wsConn.WriteMessage(websocket.BinaryMessage, buf[:length]);err != nil{
				log.Print(id, " ws write err: ", err)
				tcpConn.Close()
				wsConn.Close()
				return
			}
			if !isServer {
				log.Print(id, " send: ", length)	
			}
		}
	}
}

func ReadWs2Tcp(id string, tcpConn net.Conn, wsConn *websocket.Conn) {
	for {
		t, buf, err := wsConn.ReadMessage()
		if err != nil || t == -1 {
			log.Print(id, " ws read err: ", err)
			wsConn.Close()
			tcpConn.Close()
			return
		}
		if len(buf) > 0 {
			msg_type = t
			if _, err = tcpConn.Write(buf);err != nil{
				log.Print(id, " tcp write err: ", err)
				tcpConn.Close()
				wsConn.Close()
				return
			}
			if !isServer {
				log.Print(id, " recv: ", len(buf))	
			}
		}
	}
}

func RunServer(wsConn *websocket.Conn) {
	conn_num += 1
	id := strconv.Itoa(conn_num)
	log.Print("new ws conn: ", id, " ", wsConn.RemoteAddr().String())
	// call tcp
	tcpConn, err := net.Dial("tcp", tcp_addr)
	if(err != nil) {
		log.Print("connect to tcp err: ", err)
		return
	}
	
	go ReadWs2Tcp(id, tcpConn, wsConn)
	go ReadTcp2Ws(id, tcpConn, wsConn)
}

func RunClient(tcpConn net.Conn) {
	conn_num += 1
	id := strconv.Itoa(conn_num)
	log.Print("new tcp conn: ", id)
	// call ws
	wsConn, _, err := websocket.DefaultDialer.Dial(ws_addr, nil)
	if err != nil {
		log.Fatal("connect to ws err: ", err)
	}	
	
	go ReadWs2Tcp(id, tcpConn, wsConn)
	go ReadTcp2Ws(id, tcpConn, wsConn)
}



// 响应ws请求
func wsHandler(w http.ResponseWriter , r *http.Request){
	// ws协议握手
	conn, err := upgrader.Upgrade(w,r,nil)
	if err != nil{
		log.Print("ws upgrade err: ", err)
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
		}

		// 新线程hold住这条连接
		go RunClient(conn) 
	}
}


func main() {
	arg_num:=len(os.Args)
	if arg_num < 2 {
		fmt.Println("Client: ws://tcp2wsUrl localPort\nServer: ip:port tcp2wsPort")
		os.Exit(0)
	}
	
	// 第二个参数是纯数字（端口号）
	match, _ := regexp.MatchString("^ws://.*", os.Args[1])
	isServer = bool(!match)
	if isServer {
		// 服务端
		tcp_addr = os.Args[1]
		// ws server
		http.HandleFunc("/", wsHandler)
		go http.ListenAndServe("0.0.0.0:" + os.Args[2], nil)
		fmt.Println("Proxy with Nginx:\nlocation /sparkle/ {\nproxy_pass http://127.0.0.1:" + os.Args[2] + "/;\nproxy_read_timeout 3600;\nproxy_http_version 1.1;\nproxy_set_header Upgrade $http_upgrade;\nproxy_set_header Connection \"Upgrade\";\n}")
		fmt.Println("Server Started ws://0.0.0.0:" +  os.Args[2] + " -> " + os.Args[1] )
	} else {
		// 客户端
		ws_addr = os.Args[1]
		l, err := net.Listen("tcp", "0.0.0.0:" + os.Args[2])
		if err != nil {
			log.Print("create listen err: ", err)
			os.Exit(1)
		}
		go tcpHandler(l)
		fmt.Println("Client Started " +  os.Args[2] + " -> " + os.Args[1])
	}
	for {
		time.Sleep(9223372036854775807)
	}
}
