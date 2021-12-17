# Tcp over WebSocket (TCP to WebSocket)
本工具能 使用WebSocket创建隧道，实现TCP端口转发  
## 因为经常修改优化，所以请Star，不要Fork  
### 至于这样脱裤子放屁的操作有什么用？  
举个例子，一个服务器只能通过cdn的http转发，它也不能联网，这时你就可以利用此工具将需要转发的端口（比如22）转换成ws协议（http）来传输，再去Nginx里面配一个反代，那么当本客户端访问Nginx提供的服务的特定路径时将反代到本服务端，实现内网穿透进行端口转发  
这时防火墙仅仅发现你连接了一个WebSocket而已

## 如何使用
在右边Releases中选择你使用的平台的程序来运行  
服务端：  
`tcp2ws 要代理的ip:端口 本ws服务的监听端口`   
`tcp2ws 本地端口 本ws服务的监听端口`  
客户端：  
`tcp2ws ws://链接 本地监听端口`  
`tcp2ws http://链接 本地监听端口`  

另外也可以使用wss(https ssl)协议，ssl更为安全，但需要消耗更多流量，需要指定证书路径，另外顺带提一下nginx可以把wss(https)转发到ws(http)  
服务端：  
`tcp2ws 要代理的ip:端口 本ws服务的监听端口 证书.crt 证书.key`
`tcp2ws 本地端口 本ws服务的监听端口 证书.crt 证书.key`   
使用默认的文件名 server.crt server.key（这里的wss也可以是https或ssl）   
`tcp2ws 要代理的ip:端口 本ws服务的监听端口 wss`  
客户端：  
`tcp2ws wss://链接 本地监听端口`  
`tcp2ws https://链接 本地监听端口`  

生成自签证书的方法（一路回车即可）：  
openssl genrsa -out server.key 2048
openssl ecparam -genkey -name secp384r1 -out server.key
openssl req -new -x509 -sha256 -key server.key -out server.crt -days 36500

举个🌰：  
在服务器运行`tcp2ws 127.0.0.1:22 22222`  
然后在nginx中反代了一下  
在客户端运行`tcp2ws ws://yourdomain.com/ssh 222`  
那么就可以通过客户端的222来访问服务器的ssh啦  
是不是特别棒呢  

## 速度
在乞丐版M1 Pro的macOS下使用本工具来回转换iperf3端口测试得到的数据
```
[ ID] Interval           Transfer     Bitrate
[  5]   0.00-1.00   sec   988 MBytes  8.28 Gbits/sec
[  5]   1.00-2.00   sec   977 MBytes  8.19 Gbits/sec
[  5]   2.00-3.00   sec   982 MBytes  8.23 Gbits/sec
[  5]   3.00-4.00   sec   994 MBytes  8.34 Gbits/sec
[  5]   4.00-5.00   sec   966 MBytes  8.10 Gbits/sec
[  5]   5.00-6.00   sec   982 MBytes  8.24 Gbits/sec
[  5]   6.00-7.00   sec   989 MBytes  8.30 Gbits/sec
[  5]   7.00-8.00   sec   935 MBytes  7.84 Gbits/sec
[  5]   8.00-9.00   sec  1004 MBytes  8.42 Gbits/sec
[  5]   9.00-10.00  sec   984 MBytes  8.26 Gbits/sec
- - - - - - - - - - - - - - - - - - - - - - - - -
[ ID] Interval           Transfer     Bitrate
[  5]   0.00-10.00  sec  9.57 GBytes  8.22 Gbits/sec                  sender
[  5]   0.00-10.00  sec  9.56 GBytes  8.21 Gbits/sec                  receiver
```
走wss，因为ssl，速度肉眼可见下降:
```
[ ID] Interval           Transfer     Bitrate
[  5]   0.00-10.00  sec  7.38 GBytes  6.34 Gbits/sec                  sender
[  5]   0.00-10.00  sec  7.38 GBytes  6.33 Gbits/sec                  receiver
```
直连
```
[ ID] Interval           Transfer     Bitrate
[  5]   0.00-10.00  sec   119 GBytes   102 Gbits/sec                  sender
[  5]   0.00-10.00  sec   119 GBytes   102 Gbits/sec                  receiver
```
在i7-8550u的Windows下使用本工具来回转换iperf3端口测试得到的数据
```
[ ID] Interval           Transfer     Bandwidth
[  4]   0.00-1.00   sec   300 MBytes  2.52 Gbits/sec
[  4]   1.00-2.00   sec   336 MBytes  2.81 Gbits/sec
[  4]   2.00-3.00   sec   320 MBytes  2.68 Gbits/sec
[  4]   3.00-4.00   sec   317 MBytes  2.66 Gbits/sec
[  4]   4.00-5.00   sec   302 MBytes  2.53 Gbits/sec
[  4]   5.00-6.00   sec   328 MBytes  2.75 Gbits/sec
[  4]   6.00-7.00   sec   312 MBytes  2.61 Gbits/sec
[  4]   7.00-8.00   sec   319 MBytes  2.67 Gbits/sec
[  4]   8.00-9.00   sec   322 MBytes  2.70 Gbits/sec
[  4]   9.00-10.00  sec   348 MBytes  2.92 Gbits/sec
- - - - - - - - - - - - - - - - - - - - - - - - -
[ ID] Interval           Transfer     Bandwidth
[  4]   0.00-10.00  sec  3.14 GBytes  2.70 Gbits/sec                  sender
[  4]   0.00-10.00  sec  3.13 GBytes  2.69 Gbits/sec                  receiver
```
两个iperf3直连
```
[ ID] Interval           Transfer     Bandwidth
[  4]   0.00-10.00  sec  9.53 GBytes  8.19 Gbits/sec                  sender
[  4]   0.00-10.00  sec  9.53 GBytes  8.19 Gbits/sec                  receiver
```

## 伪装
在直接访问监听端口的任意路径，默认会返回一个空白页面  
可以写一个`index.html`放到运行目录下来代替这个空白页面
直接访问时就会显示这个文件的内容，伪装成一个非常普通的Web服务  
推荐用一个叫SingleFile的插件可以把页面直接存成一个文件