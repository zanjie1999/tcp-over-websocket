// Tcp over WebSocket (tcp2ws)
// 基于ws的内网穿透工具
// Sparkle 20210430
// v2.0

package main

import (
	"github.com/gorilla/websocket"
	"log"
	"net"
	"net/http"
	"os"
	"fmt"
	"regexp"
)

var (
	tcp_addr string
	ws_addr string
	conn_num int
	msg_type int
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool{ return true },
}

func ReadTcp2Ws(id string, tcpConn net.Conn, wsConn *websocket.Conn) {
	buf := make([]byte, 1024)
	length,err := tcpConn.Read(buf)
	if err != nil {
		log.Print(id, "tcp read err", err)
		wsConn.Close()
		tcpConn.Close()
		boom <- 1
		return
	}
	if length > 0 {
		if err = wsConn.WriteMessage(websocket.BinaryMessage, buf[:length]);err != nil{
			log.Print(id, "ws write err", err)
			tcpConn.Close()
			wsConn.Close()
			boom <- 1
			return
		}
		log.Print(id, "recv tcp : ", string(buf[:length]))
	}
}

func ReadWs2Tcp(id string, tcpConn net.Conn, wsConn *websocket.Conn) {
	t, buf, err := wsConn.ReadMessage()
	if err != nil || t == -1 {
		log.Print(id, "ws read err", err)
		wsConn.Close()
		tcpConn.Close()
		boom <- 1
		return
	}
	if len(buf) > 0 {
		msg_type = t
		if _, err = tcpConn.Write(buf);err != nil{
			log.Print(id, "tcp write err", err)
			tcpConn.Close()
			wsConn.Close()
			boom <- 1
			return
		}
		log.Print(id, "recv ws : ", string(buf))		
	}
}

func RunServer(wsConn *websocket.Conn) {
	conn_num += 1
	id := string(conn_num)
	log.Print("new ws conn ", id, wsConn.RemoteAddr().String)
	tcpConn, err := net.Dial("tcp", tcp_addr)
	if(err != nil) {
		log.Print("connect to tcp", err)
		return
	}
	
	for {
		boom = make(chan int)
		go ReadWs2Tcp(id, tcpConn, wsConn)
		ReadTcp2Ws(id, tcpConn, wsConn)
		if <- boom.After(1) {
			break
		}
	}
}

func RunConnect(tcpConn net.Conn) {
	conn_num += 1
	id := string(conn_num)
	log.Print("new tcp conn ", id)
	tcpConn, err := net.Dial("tcp", tcp_addr)
	if(err != nil) {
		log.Print("connect to tcp", err)
		return
	}
	
	// for {
	// 	if !ReadWs2Tcp(id, tcpConn, wsConn) || !ReadTcp2Ws(id, tcpConn, wsConn) {
	// 		break
	// 	}
	// }
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
	conn, err := listener.Accept()
	if err != nil {
		log.Print("tcp accept err: ", err)
	}

	// 新线程hold住这条连接
	go RunConnect(conn) 
}


func main() {
	arg_num:=len(os.Args)
	if arg_num < 2 {
		fmt.Println("Connect: port tcp2wsUrl\nServer: ip:port tcp2wsPort")
		fmt.Print("\nProxy with Nginx:\nlocation /sparkle {\nproxy_pass http://127.0.0.1:tcp2wsPort;\nproxy_http_version 1.1;\nproxy_set_header Upgrade $http_upgrade;\nproxy_set_header Connection \"Upgrade\";\n}")
		os.Exit(0)
	}
	
	// 第二个参数是纯数字（端口号）
	match, _ := regexp.MatchString("[0-9]+", os.Args[2])
	if match {
		// 服务端
		tcp_addr = os.Args[1]
		// ws server
		http.HandleFunc("/", wsHandler)
		go http.ListenAndServe("0.0.0.0:" + os.Args[2], nil)
		fmt.Println("Started ws://0.0.0.0:" +  os.Args[2] + " -> " + os.Args[1] )
	} else {
		// 客户端
		ws_addr = os.Args[1]
		l, err := net.Listen("tcp", "0.0.0.0:" + os.Args[2])
		if err != nil {
			log.Print("create listen err", err)
			os.Exit(1)
		}
		go tcpHandler(l)
		fmt.Println("Started " +  os.Args[1] + " -> ws://0.0.0.0:" + os.Args[2])
	}
	for {}
}
