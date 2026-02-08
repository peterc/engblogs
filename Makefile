.PHONY: build render dev clean

build:
	go run main.go

render:
	go run main.go -skip-fetch

dev: render
	@echo "Serving public/ at http://localhost:8080"
	@cd public && python3 -m http.server 8080

clean:
	rm -rf public cache.json
