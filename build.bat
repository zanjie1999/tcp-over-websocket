rd /s /q build
mkdir build
SET CGO_ENABLED=1
SET GOARCH=amd64
SET GOOS=windows
go build
move tcp2ws.exe build\tcp2ws.exe
SET GOARCH=386
go build
move tcp2ws.exe build\tcp2ws-i386.exe
SET CGO_ENABLED=0
SET GOOS=linux
go build
move tcp2ws build\tcp2ws-linux-i386
SET GOARCH=amd64
go build
zip tcp2ws-linux.zip tcp2ws
cp kazari.png build\tcp2ws-zip-linux.png
cat tcp2ws-linux.zip >> build\tcp2ws-zip-linux.png
rm tcp2ws-linux.zip
move tcp2ws build\tcp2ws-linux
SET GOARCH=arm
go build
move tcp2ws build\tcp2ws-linux-arm
SET GOARCH=mips
go build
move tcp2ws build\tcp2ws-linux-mips
SET GOARCH=arm64
go build
move tcp2ws build\tcp2ws-linux-arm64
SET GOOS=darwin
go build
move tcp2ws build\tcp2ws-darwin-arm64
SET GOARCH=amd64
go build
move tcp2ws build\tcp2ws-darwin
SET GOOS=freebsd
go build
move tcp2ws build/tcp2ws-freebsd