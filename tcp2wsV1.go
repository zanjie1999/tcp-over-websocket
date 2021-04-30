// Tcp over WebSocket (tcp2ws)
// 基于ws的内网穿透工具
// Sparkle 20210430
// v1.0

package main

import (
    "github.com/gorilla/websocket"
    "io"
    "log"
    "net"
    "os"
    "fmt"
    "bufio"
    "bytes"
    "strconv"
    "strings"
    "crypto/sha1"
    "encoding/base64"
    "encoding/binary"
    "regexp"
)

type WebSocket struct {
    Listener    net.Listener
    Clients        []*WsClient
}

var num int
var tcp_addr string
type WsClient struct {
    Conn         net.Conn
    Shook        bool
    Server        *WebSocket
    TcpConn      net.Conn
    WebsocketType   int
    Num             int
}


type TCPServer struct {
    Listener    net.Listener
    Clients        []*TcpClient
}

var ws_addr string
type TcpClient struct {
    Conn         net.Conn
    Shook        bool
    Server        *WebSocket
    TcpConn      net.Conn
    WebsocketType   int
    Num             int
}

type Msg struct {
    Data        string
    Num            int
}

func (self *WsClient) Release() {
    // release all connect
    self.TcpConn.Close()
    self.Conn.Close()
}


func (self *WsClient) HandleServer() {
    defer self.Release()
    if !self.Handshake() {
        // handshak err , del this conn
        return
    }

    // connect to another server for tcp
    if !self.ConnTcpServer(){
        // can not connect to the other server , release
        return
    }
    num = num + 1
    log.Print("now connect num : ", num)
    self.Num = num
    go self.ReadWs()
    self.ReadTcp()
}

func (self *WsClient) ReadWs() {
    var (
        buf     []byte
        err     error
        rsv     byte
        opcode    byte
        mask    byte
        mKey    []byte
        length    uint64
        l        uint16
        payload    byte
    )
    for {
        buf = make([]byte, 2)
        _, err = io.ReadFull(self.Conn, buf)
        if err != nil {
            self.Release()
            break
        }

        rsv = (buf[0] >>4) &0x7
        // which must be 0
        if rsv != 0{
            log.Print("WsClient send err msg:",rsv,", disconnect it")
            self.Release()
            break
        }

        opcode = buf[0] & 0xf
        // opcode   if 8 then disconnect
        if opcode == 8 {
            log.Print("CLient want close Connection")
            self.Release()
            break
        }

        // should save the opcode 
        // if client send by binary should return binary (especially for Egret)
        self.WebsocketType = int(opcode)

        mask = buf[1] >> 7
        // the translate may have mask 

        payload = buf[1] & 0x7f
        // if length < 126 then payload mean the length
        // if length == 126 then the next 8bit mean the length
        // if length == 127 then the next 64bit mean the length
        switch {
        case payload < 126:
            length = uint64(payload)

        case payload == 126:
            buf = make([]byte, 2)
            io.ReadFull(self.Conn, buf)
            binary.Read(bytes.NewReader(buf), binary.BigEndian, &l)
            length = uint64(l)

        case payload == 127:
            buf = make([]byte, 8)
            io.ReadFull(self.Conn, buf)
            binary.Read(bytes.NewReader(buf), binary.BigEndian, &length)
        }
        if mask == 1 {
            mKey = make([]byte, 4)
            io.ReadFull(self.Conn, mKey)
        }
        buf = make([]byte, length)
        io.ReadFull(self.Conn, buf)
        if mask == 1 {
            for i, v := range buf {
                buf[i] = v ^ mKey[i % 4]
            }
            //fmt.Print("mask", mKey)
        }
        log.Print("rec from the client(",self.Num,")", string(buf))
        self.TcpConn.Write(buf)
    }
}
// read from other tcp
func (self *WsClient) ReadTcp() {
    var (
        buf  []byte
    )
    buf = make([]byte, 1024)

    for {
        length,err := self.TcpConn.Read(buf)

        if err != nil {
            self.Release()
            num = num - 1
            // only 
            log.Print("other tcp connect err", err)
            log.Print("disconnect client :", self.Num)
            log.Print("now have:", num)
            break
        }
        log.Print("recv from other tcp : ", string(buf[:length]))
        self.WriteWs(buf[:length])
        //Write to websocket
    }
}

// write to websocket
func (self *WsClient) WriteWs(data []byte) bool {
    data_binary := new(bytes.Buffer) //which 

    //should be binary or string
    frame := []byte{129}  //string
    length := len(data)
    // 10000001
    if self.WebsocketType == 2 {
        frame = []byte{130}
        // 10000010
        err := binary.Write(data_binary, binary.LittleEndian, data)
        if err != nil {
            log.Print(" translate to binary err", err)
        }
        length = len(data_binary.Bytes())
    }
    switch {
    case length < 126:
        frame = append(frame, byte(length))
    case length <= 0xffff:
        buf := make([]byte, 2)
        binary.BigEndian.PutUint16(buf, uint16(length))
        frame = append(frame, byte(126))
        frame = append(frame, buf...)
    case uint64(length) <= 0xffffffffffffffff:
        buf := make([]byte, 8)
        binary.BigEndian.PutUint64(buf, uint64(length))
        frame = append(frame, byte(127))
        frame = append(frame, buf...)
    default:
        log.Print("Data too large")
        return false
    }
    if self.WebsocketType == 2 {
        frame = append(frame, data_binary.Bytes()...)
    } else {
        frame = append(frame, data...)
    }
    self.Conn.Write(frame)
    frame = []byte{0}
    return true
}

func (self *WsClient) ConnTcpServer() bool {

    conn, err := net.Dial("tcp", tcp_addr)

    if(err != nil) {
        log.Print("connect other tcp server error")
        return false
    }

    self.TcpConn = conn
    return true
}

func NewTcpServer(addr string) *TCPServer {
    l,err:=net.ResolveTCPAddr("tcp", addr)
	conn,err:=net.DialTCP("tcp",nil,tcpAddr)
    if err != nil {
        log.Fatal(err)
        return nil;
    }
    return &WebSocket{l, make([]*WsClient, 0)}
}

func (self *WsClient) Handshake() bool {
    if self.Shook {
        return true
    }
    reader := bufio.NewReader(self.Conn)
    key := ""
    str := ""
    for {
        line, _, err := reader.ReadLine()
        if err != nil {
            log.Print("Handshake err:",err)
            return false
        }
        if len(line) == 0 {
            break
        }
        str = string(line)
        if strings.HasPrefix(str, "Sec-WebSocket-Key") {
            if len(line)>= 43 {
                key = str[19:43]
            }
        }
    }
    if key == "" {
        return false
    }
    sha := sha1.New()
    io.WriteString(sha, key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
    key = base64.StdEncoding.EncodeToString(sha.Sum(nil))
    header := "HTTP/1.1 101 Switching Protocols\r\n" +
    "Connection: Upgrade\r\n" +
    "Sec-WebSocket-Version: 13\r\n" +
    "Sec-WebSocket-Accept: " + key + "\r\n" +
    "Upgrade: websocket\r\n\r\n"
    self.Conn.Write([]byte(header))
    self.Shook = true
    self.Server.Clients = append(self.Server.Clients, self)
    return true
}

func NewWebSocketServer(addr string) *WebSocket {
    l, err := net.Listen("tcp", addr)
    if err != nil {
        log.Fatal(err)
        os.Exit(1)
    }
    return &WebSocket{l, make([]*WsClient, 0)}
}

func NewWebSocketConnect(addr string) *WebSocket {
    l,err:=net.ResolveTCPAddr("tcp", addr)
	conn,err:=net.DialTCP("tcp",nil,tcpAddr)
    if err != nil {
        log.Fatal(err)
        return nil;
    }
    return &WebSocket{l, make([]*WsClient, 0)}
}

func (self *WebSocket) WsServerLoop() {
    for {
        conn, err := self.Listener.Accept()
        if err != nil {
            log.Print("client conn err:", err)
            continue
        }
        s := conn.RemoteAddr().String()
        i, _ := strconv.Atoi(strings.Split(s, ":")[1])
        client := &WsClient{conn, "", false, self, i, conn, 1, num}
        go client.HandleServer()
    }
}


func main() {
    arg_num:=len(os.Args)
    if arg_num < 2 {
        fmt.Println("Connect: port ip:port\nServer: ip:port port")
		fmt.Print("\nProxy with Nginx:\nlocation /sparkle {\nproxy_pass http://127.0.0.1:port;\nproxy_http_version 1.1;\nproxy_set_header Upgrade $http_upgrade;\nproxy_set_header Connection \"Upgrade\";\n}")
        os.Exit(0)
    }
	
    // 第二个参数是数字（端口号）
    match, _ := regexp.MatchString("[0-9]+", os.Args[2])
	if match {
		// 服务端
		num = 0
		tcp_addr = os.Args[1]
        // ws server
        ws := NewWebSocketServer("0.0.0.0:" + os.Args[2])
		go ws.WsServerLoop()
		fmt.Println("Started " +  os.Args[1] + " -> ws://0.0.0.0:" + os.Args[2])
	} else {
		// 客户端
        ws := NewWebSocketServer("0.0.0.0:" + os.Args[2])

        fmt.Println("Started ws://" +  os.Args[1] + " -> 0.0.0.0:" + os.Args[2])
	}
    // walt for run
    for {}
}