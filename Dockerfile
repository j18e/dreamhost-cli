FROM alpine:3.10

COPY ./dreamhost-cli .

ENTRYPOINT ./dreamhost-cli
