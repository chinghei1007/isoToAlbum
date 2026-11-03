.PHONY: setup check-deps frontend build test e2e run clean

setup:
	./scripts/setup.sh

check-deps:
	./scripts/setup.sh --check

frontend:
	npm --prefix frontend ci
	npm --prefix frontend run build

build:
	./scripts/build.sh

test: frontend
	go test ./...
	go vet ./...

e2e:
	./scripts/e2e.sh

run: build
	./bin/iso-to-album

clean:
	go clean
	rm -rf bin frontend/dist web/dist/assets

