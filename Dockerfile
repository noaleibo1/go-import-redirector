FROM golang:1.8.3-alpine

RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*

WORKDIR /root/

ADD bin/go-import-redirector.linux go-import-redirector
ADD config_imports.txt .

CMD ["./go-import-redirector"]