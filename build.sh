cd `dirname $0`
rm -rf build
mkdir build
export CGO_ENABLED=1
export GOARCH=amd64
export GOOS=windows
go build
mv tcp2ws.exe build/tcp2ws.exe
export CGO_ENABLED=0
export GOOS=darwin
go build
mv tcp2ws build/tcp2ws-darwin
export GOOS=linux
go build
zip tcp2ws-linux.zip tcp2ws
cp kazari.png build/tcp2ws-zip-linux.png
cat tcp2ws-linux.zip >> build/tcp2ws-zip-linux.png
rm tcp2ws-linux.zip
mv tcp2ws build/tcp2ws-linux
export GOARCH=arm64
go build
mv tcp2ws build/tcp2ws-linux-arm64
export GOOS=darwin
go build
mv tcp2ws build/tcp2ws-darwin-arm64
