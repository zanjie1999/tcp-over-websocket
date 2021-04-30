# Tcp over WebSocket (TCP to WebSocket)
本工具能 使用WebSocket创建隧道，实现TCP端口转发  
### 至于这样脱裤子放屁的操作有什么用？  
举个例子，一个服务器只能通过cdn的http转发，它也不能联网，这时你就可以利用此工具将需要转发的端口（比如22）转换成ws协议（http）来传输，再去Nginx里面配一个反代，那么当本客户端访问Nginx提供的服务的特定路径时将反代到本服务端，实现内网穿透进行端口转发  
这时防火墙仅仅发现你连接了一个WebSocket而已
