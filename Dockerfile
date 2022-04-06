FROM golang:1.17 AS builder

ENV GOOS=linux
ENV GOARCH=386

WORKDIR /work

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY main.go .
RUN go build -o ./dreamhost-cli

FROM alpine:3.14

COPY --from=builder /work/dreamhost-cli .

ENTRYPOINT ["./dreamhost-cli"]
