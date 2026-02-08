.PHONY: build dev clean

build:
	go run main.go

dev: build
	@echo "Serving public/ at http://localhost:8080"
	@cd public && python3 -m http.server 8080

clean:
	rm -rf public cache.json
