del tcp2ws-windows.exe
del tcp2ws-linux
del tcp2ws-linux-arm64
del kazari-linux.png
del tcp2ws-darwin
SET CGO_ENABLED=1
SET GOARCH=amd64
SET GOOS=windows
go build
rename tcp2ws.exe tcp2ws-windows.exe
SET CGO_ENABLED=0
SET GOOS=darwin
go build
rename tcp2ws tcp2ws-darwin
SET GOOS=linux
go build
rename tcp2ws tcp2ws-linux
SET GOARCH=arm64
go build
rename tcp2ws tcp2ws-linux-arm64

7z a tcp2ws-linux.zip tcp2ws-linux
copy /b kazari.png+tcp2ws-linux.zip tcp2ws-zip-linux.png
del tcp2ws-linux.zip