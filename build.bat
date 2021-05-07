del tcp2ws-windows.exe
del tcp2ws-linux
del tcp2ws-darwin
SET CGO_ENABLED=1
SET GOARCH=
SET GOOS=windows
go build -ldflags "-H windowsgui"
rename tcp2ws.exe tcp2ws-windows.exe
SET CGO_ENABLED=0
SET GOARCH=amd64
SET GOOS=linux
go build
rename tcp2ws tcp2ws-linux
SET GOOS=darwin
SET GOARCH=amd64
go build
rename tcp2ws tcp2ws-darwin