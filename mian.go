// Tcp over WebSocket (tcp2ws)
// 基于ws的内网穿透工具
// Sparkle 20210430
// v3.0

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
	"strconv"
	"time"
)

type tcp2wsSparkle struct {
	id string
	tcpConn net.Conn
	wsConn *websocket.Conn
	uuid string
 }

var (
	tcp_addr string
	ws_addr string
	conn_num int
	msg_type int = websocket.BinaryMessage
	isServer bool
	connMap map[string]*tcp2wsSparkle = make(map[string]*tcp2wsSparkle)
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool{ return true },
}

func deleteConnMap(uuid string) {
	if _, haskey := connMap[uuid]; haskey && connMap[uuid] != nil{
		if connMap[uuid].tcpConn != nil {
			connMap[uuid].tcpConn.Close()
		}
		if connMap[uuid].wsConn != nil {
			log.Print("say bye to ", uuid)
			connMap[uuid].wsConn.WriteMessage(websocket.TextMessage, []byte("tcp2wsSparkleClose"))
			connMap[uuid].wsConn.Close()
		}
		delete(connMap, uuid)
	}
}

func ReadTcp2Ws(uuid string) (bool) {
	if _, haskey := connMap[uuid]; !haskey {
		return false
	}
	id := connMap[uuid].id
	buf := make([]byte, 16392)
	tcpConn := connMap[uuid].tcpConn
	for {
		length,err := tcpConn.Read(buf)
		if err != nil {
			log.Print(id, " tcp read err: ", err)
			deleteConnMap(uuid)
			return false
		}
		if length > 0 {
			// 因为tcpConn.Read会阻塞 所以要从connMap中获取最新的wsConn
			wsConn := connMap[uuid].wsConn
			if err = wsConn.WriteMessage(msg_type, buf[:length]);err != nil{
				log.Print(id, " ws write err: ", err)
				// tcpConn.Close()
				wsConn.Close()
				return true
			}
			if !isServer {
				log.Print(id, " send: ", length)	
			}
		}
	}
}

func ReadWs2Tcp(uuid string) (bool) {
	if _, haskey := connMap[uuid]; !haskey {
		return false
	}
	id := connMap[uuid].id
	wsConn := connMap[uuid].wsConn
	tcpConn := connMap[uuid].tcpConn
	for {
		t, buf, err := wsConn.ReadMessage()
		if err != nil || t == -1 {
			log.Print(id, " ws read err: ", err)
			wsConn.Close()
			// tcpConn.Close()
			return true
		}
		if len(buf) > 0 {
			if t == websocket.TextMessage {
				msg := string(buf)
				if msg == "tcp2wsSparkle" {
					log.Print("yay")
					continue
				} else if msg == "tcp2wsSparkleClose" {
					log.Print("ws say bye ", uuid)
					wsConn.Close()
					tcpConn.Close()
					delete(connMap, uuid)
					return false
				}
			}
			msg_type = t
			if _, err = tcpConn.Write(buf);err != nil{
				log.Print(id, " tcp write err: ", err)
				deleteConnMap(uuid)
				return false
			}
			if !isServer {
				log.Print(id, " recv: ", len(buf))	
			}
		}
	}
}

func ReadWs2TcpClient(uuid string) {
	if ReadWs2Tcp(uuid) {
		// error return  re call ws
		RunClient(nil, uuid)
	}
}

func RunServer(wsConn *websocket.Conn) {
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
			if _, haskey := connMap[uuid]; haskey {
				tcpConn = connMap[uuid].tcpConn
				connMap[uuid].wsConn.Close()
				connMap[uuid].wsConn = wsConn
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
			conn_num += 1
			id := strconv.Itoa(conn_num)
			connMap[uuid] = &tcp2wsSparkle {id, tcpConn, wsConn, uuid}
		}
	} else {
		log.Print("uuid finded ", uuid)
	}
	
	go ReadWs2Tcp(uuid)
	go ReadTcp2Ws(uuid)
}

func RunClient(tcpConn net.Conn, uuid string) {
	log.Print("dial ws ", uuid)
	// call ws
	wsConn, _, err := websocket.DefaultDialer.Dial(ws_addr, nil)
	if err != nil {
		log.Print("connect to ws err: ", err)
		tcpConn.Close()
		return
	}
	// send uuid
	if err := wsConn.WriteMessage(websocket.TextMessage, []byte(uuid));err != nil{
		log.Print("send ws uuid err: ", err)
		tcpConn.Close()
		wsConn.Close()
		return
	}
	
	// save conn
	if tcpConn != nil {
		// save
		conn_num += 1
		id := strconv.Itoa(conn_num)
		connMap[uuid] = &tcp2wsSparkle {id, tcpConn, wsConn, uuid}
	} else {
		// update
		if _, haskey := connMap[uuid]; haskey {
			connMap[uuid].wsConn.Close()
			connMap[uuid].wsConn = wsConn
		}
	}

	go ReadWs2TcpClient(uuid)
	if tcpConn != nil {
		go ReadTcp2Ws(uuid)
	}
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

		log.Print("new tcp conn: ")

		// 新线程hold住这条连接
		go RunClient(conn, uuid.New().String())
	}
}


func main() {
	arg_num:=len(os.Args)
	if arg_num < 2 {
		fmt.Println("Client: ws://tcp2wsUrl localPort\nServer: ip:port tcp2wsPort")
		os.Exit(0)
	}
	
	// 第二个参数是纯数字（端口号）
	match, _ := regexp.MatchString("^(ws|http)://.*", os.Args[1])
	isServer = bool(!match)
	if isServer {
		// 服务端
		tcp_addr = os.Args[1]
		// ws server
		http.HandleFunc("/", wsHandler)
		go http.ListenAndServe("0.0.0.0:" + os.Args[2], nil)
		fmt.Println("Proxy with Nginx:\nlocation /sparkle/ {\nproxy_pass http://127.0.0.1:" + os.Args[2] + "/;\nproxy_read_timeout 3600;\nproxy_http_version 1.1;\nproxy_set_header Upgrade $http_upgrade;\nproxy_set_header Connection \"Upgrade\";\n}")
		fmt.Println("Server Started ws://0.0.0.0:" +  os.Args[2] + " -> " + tcp_addr )
	} else {
		// 客户端
		if match, _ := regexp.MatchString("^http://.*", os.Args[1]); match {
			ws_addr = "ws" + os.Args[1][4:]
		} else {
			ws_addr = os.Args[1]
		}
		l, err := net.Listen("tcp", "0.0.0.0:" + os.Args[2])
		if err != nil {
			log.Print("create listen err: ", err)
			os.Exit(1)
		}
		go tcpHandler(l)
		fmt.Println("Client Started " +  os.Args[2] + " -> " + ws_addr)
	}
	for {
		if isServer {
			time.Sleep(5 * 60 * time.Second)
			// check ws
			for k, i := range connMap {
				if err := i.wsConn.WriteMessage(websocket.TextMessage, []byte("tcp2wsSparkle"));err != nil{
					log.Print(i.id, " timeout close")
					i.tcpConn.Close()
					i.wsConn.Close()
					deleteConnMap(k)
				}
			}
		} else {
			time.Sleep(9223372036854775807)
		}
	}
}
