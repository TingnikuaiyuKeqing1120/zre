APP_NAME := zre
VERSION := 0.1.2

.PHONY: build build-tray run test vet clean release

LDFLAGS := -s -w -X main.version=$(VERSION)

# 控制台版（终端里运行，日志可见）
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME).exe ./cmd/zre

# 托盘版（无控制台窗口，双击即进托盘；托盘菜单可打开页面 / 退出）
build-tray:
	go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui -X main.defaultTray=true" -o bin/$(APP_NAME)-tray.exe ./cmd/zre

run:
	go run ./cmd/zre

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin/ dist/

release: test
	mkdir -p dist
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-$(VERSION)-windows-amd64.exe ./cmd/zre
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS) -H windowsgui -X main.defaultTray=true" -o dist/$(APP_NAME)-$(VERSION)-windows-tray-amd64.exe ./cmd/zre
	GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-$(VERSION)-darwin-amd64 ./cmd/zre
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-$(VERSION)-darwin-arm64 ./cmd/zre
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-$(VERSION)-linux-amd64 ./cmd/zre
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-$(VERSION)-linux-arm64 ./cmd/zre
