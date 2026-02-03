build-linux:
	GOOS=linux GOARCH=amd64 go build -o spotdl-wapper

deploy:
	scp spotdl-wapper root@music-services:/root/
