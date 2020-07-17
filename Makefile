NAME := dreamhost-cli
IMAGE_NAME := j18e/$(NAME)

COMMIT_HASH := $(shell git rev-parse --short HEAD)
IMAGE_FULL := $(IMAGE_NAME):$(COMMIT_HASH)

build:
	GOOS=linux GOARCH=386 go build -o ./$(NAME) .

docker-build:
	docker build -t $(IMAGE_FULL) .
	docker tag $(IMAGE_FULL) $(IMAGE_NAME):latest

docker-push:
	docker push $(IMAGE_FULL)
	docker push $(IMAGE_NAME):latest

all: build docker-build docker-push
