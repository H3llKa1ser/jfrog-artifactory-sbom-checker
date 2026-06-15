#!/bin/bash

# Windows
GOOS=windows GOARCH=amd64 go build -o build/jfrog_sbom_package_checker_windows_amd64.exe jfrog_sbom_package_checker.go
GOOS=windows GOARCH=386   go build -o build/jfrog_sbom_package_checker_windows_386.exe jfrog_sbom_package_checker.go
GOOS=windows GOARCH=arm64 go build -o build/jfrog_sbom_package_checker_windows_arm64.exe jfrog_sbom_package_checker.go

# Linux
GOOS=linux GOARCH=amd64 go build -o build/jfrog_sbom_package_checker_linux_amd64 jfrog_sbom_package_checker.go
GOOS=linux GOARCH=arm64 go build -o build/jfrog_sbom_package_checker_linux_arm64 jfrog_sbom_package_checker.go

# macOS
GOOS=darwin GOARCH=amd64 go build -o build/jfrog_sbom_package_checker_macos_amd64 jfrog_sbom_package_checker.go
GOOS=darwin GOARCH=arm64 go build -o build/jfrog_sbom_package_checker_macos_arm64 jfrog_sbom_package_checker.go

echo "✅ All builds complete in ./build/"
