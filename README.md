# Restful api for VTR
Restful api for VTR Database as service (frontend)

## How to Build
```sh
$ git clone https://adc.github.trendmicro.com/phina-wang/vtr_gin.git
$ cd vtr_gin
$ make
```

## How to Build with docker
```sh
$ docker build --build-arg "http_proxy=http://sjc1-prxy.sdi.trendnet.org:8080" --build-arg "https_proxy=http://sjc1-prxy.sdi.trendnet.org:8080" -t vtr_gin:0.2 .
```

## Test with docker
```sh
$ docker run -d -v $(pwd)/config/config.toml:/etc/gin/config.toml -p 80:8000 vtr_gin:0.2
```

