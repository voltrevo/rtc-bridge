.PHONY: all clean client coordinator node echo

all: coordinator node echo

# Build the JS bundle (required before coordinator)
client:
	cd client && npm install --silent && npm run build

# coordinator embeds the JS bundle — client must build first
coordinator: client
	go build -o coordinator/coordinator ./coordinator

node:
	go build -o node/node ./node

echo:
	go build -o echo/echo ./echo

clean:
	rm -f coordinator/coordinator node/node echo/echo
	rm -f coordinator/static/rtc-bridge.js coordinator/static/rtc-bridge.js.map
