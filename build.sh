#!/bin/bash

# Windows
GOOS=windows GOARCH=amd64 go build -o build/check_packages_windows_amd64.exe check_packages.go
GOOS=windows GOARCH=386   go build -o build/check_packages_windows_386.exe check_packages.go
GOOS=windows GOARCH=arm64 go build -o build/check_packages_windows_arm64.exe check_packages.go

# Linux
GOOS=linux GOARCH=amd64 go build -o build/check_packages_linux_amd64 check_packages.go
GOOS=linux GOARCH=arm64 go build -o build/check_packages_linux_arm64 check_packages.go

# macOS
GOOS=darwin GOARCH=amd64 go build -o build/check_packages_macos_amd64 check_packages.go
GOOS=darwin GOARCH=arm64 go build -o build/check_packages_macos_arm64 check_packages.go

echo "✅ All builds complete in ./build/"
