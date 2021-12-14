# Tcp over WebSocket (TCP to WebSocket)
本工具能 使用WebSocket创建隧道，实现TCP端口转发  
## 因为经常修改优化，所以请Star，不要Fork  
### 至于这样脱裤子放屁的操作有什么用？  
举个例子，一个服务器只能通过cdn的http转发，它也不能联网，这时你就可以利用此工具将需要转发的端口（比如22）转换成ws协议（http）来传输，再去Nginx里面配一个反代，那么当本客户端访问Nginx提供的服务的特定路径时将反代到本服务端，实现内网穿透进行端口转发  
这时防火墙仅仅发现你连接了一个WebSocket而已

## 如何使用
在右边Releases中选择你使用的平台的程序来运行  
服务端：`tcp2ws 要代理的ip:端口 本ws服务的监听端口`  
客户端：`tcp2ws ws://链接 本地监听端口`  

举个🌰：  
在服务器运行`tcp2ws 127.0.0.1:22 22222`  
然后在nginx中反代了一下  
在客户端运行`tcp2ws ws://yourdomain.com/ssh 222`  
那么就可以通过客户端的222来访问服务器的ssh啦  
是不是特别棒呢  

## 速度
在Windows使用本工具来回转换iperf3端口测试得到的数据
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