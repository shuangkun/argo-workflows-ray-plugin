FROM golang:1.23 AS builder

WORKDIR /go/src/github.com/shuangkun/argo-workflows-ray-plugin
COPY . /go/src/github.com/shuangkun/argo-workflows-ray-plugin
RUN go mod download
RUN CGO_ENABLED=0 go build -ldflags "-w -s" -o argo-ray-plugin main.go

FROM alpine:3.10
COPY --from=builder /go/src/github.com/shuangkun/argo-workflows-ray-plugin/argo-ray-plugin /usr/bin/argo-ray-plugin
RUN chmod +x /usr/bin/argo-ray-plugin
CMD ["argo-ray-plugin"]

