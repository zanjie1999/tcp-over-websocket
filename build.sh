cd `dirname $0`
mkdir -p build
rm -rf build/*
export CGO_ENABLED=1
export GOARCH=amd64
export GOOS=windows
go build
mv tcp2ws.exe build/tcp2ws.exe
export GOARCH=386
go build
mv tcp2ws.exe build/tcp2ws-i386.exe
export CGO_ENABLED=0
export GOOS=linux
go build
mv tcp2ws build/tcp2ws-linux-i386
export GOARCH=amd64
go build
zip tcp2ws-linux.zip tcp2ws
cp kazari.png build/tcp2ws-zip-linux.png
cat tcp2ws-linux.zip >> build/tcp2ws-zip-linux.png
rm tcp2ws-linux.zip
mv tcp2ws build/tcp2ws-linux
export GOARCH=arm
go build
mv tcp2ws build/tcp2ws-linux-arm
export GOARCH=mips
go build
mv tcp2ws build/tcp2ws-linux-mips
export GOARCH=arm64
go build
mv tcp2ws build/tcp2ws-linux-arm64
export GOOS=darwin
go build
mv tcp2ws build/tcp2ws-darwin-arm64
export GOARCH=amd64
go build
mv tcp2ws build/tcp2ws-darwin