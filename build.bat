rd /s /q build
mkdir build
SET CGO_ENABLED=1
SET GOARCH=amd64
SET GOOS=windows
go build
move tcp2ws.exe build\tcp2ws.exe
SET CGO_ENABLED=0
SET GOOS=darwin
go build
move tcp2ws build\tcp2ws-darwin
SET GOOS=linux
go build
7z\7z a tcp2ws-linux.zip tcp2ws
copy /b kazari.png+tcp2ws-linux.zip build\tcp2ws-zip-linux.png
del tcp2ws-linux.zip
move tcp2ws build\tcp2ws-linux
SET GOARCH=arm64
go build
move tcp2ws build\tcp2ws-linux-arm64
SET GOOS=darwin
go build
move tcp2ws build\tcp2ws-darwin-arm64
