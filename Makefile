GOPATH ?= ~/go
OUTPUT ?= /usr/bin/gin
PATCHDIR = ${PWD}/patch

all: deps patch build

deps:
	go get github.com/gin-gonic/gin
	go get github.com/bitly/go-simplejson
	go get gopkg.in/mgo.v2
	go get github.com/go-sql-driver/mysql
	go get github.com/BurntSushi/toml
	cd ${GOPATH}/src && patch -p1 < ${PATCHDIR}/gin.patch

build:
	go build -o ${OUTPUT} main.go v1.go
