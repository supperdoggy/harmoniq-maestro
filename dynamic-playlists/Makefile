build-linux:
	GOOS=linux GOARCH=amd64 go build -o dynamic-playlists

deploy:
	scp dynamic-playlists root@music-services:/root/
