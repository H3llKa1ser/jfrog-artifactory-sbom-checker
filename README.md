# JFrog Artifactory Package Checker

## Installation

### 1) Clone from repository

    git clone https://github.com/H3llKa1ser/jfrog-artifactory-package-checker

Then

    cd jfrog-artifactory-package-checker/

### 2) Build go file into an executable

    go build -o check_packages check_packages.go

### 3) Give the file executable permissions

    chmod +x check_packages

### 4) Move the file to a location to execute system wide

    mv check_packages /usr/bin/check_packages

## Usage

### 1) Run the tool with basic authentication

    ./check_packages -csv PACKAGE_LIST.csv -host https://artifactory.mycompany.com -user USER -pass PASSWORD -output ASSESSED_PACKAGE_LIST.csv

### 2) Run parallel HTTP requests for faster assessment time (be careful to not overload the instance if it might not handle many concurrent requests!)

Number can be from 10 (Slow) to 200+ (Fast). Default is 50.

    ./check_packages -csv PACKAGE_LIST.csv -host https://artifactory.mycompany.com -user USER -pass PASSWORD -output ASSESSED_PACKAGE_LIST.csv -workers 100

### 3) Help menu

    ./check-packages -help

## Supported Ecosystems

| Ecosystem (CSV value)      | Language / Platform             | Example Package                                    |
|----------------------------|---------------------------------|----------------------------------------------------|
| `npm`                      | JavaScript / TypeScript         | `@tanstack/react-query`                            |
| `pypi`                     | Python                          | `requests`, `flask`                                |
| `maven`                    | Java / Kotlin / Scala           | `org.apache.commons:commons-lang3`                 |
| `nuget`                    | C# / .NET                       | `Newtonsoft.Json`                                  |
| `go`                       | Go                              | `github.com/gin-gonic/gin`                         |
| `docker`                   | Containers                      | `nginx`, `redis`                                   |
| `gems` / `rubygems`         | Ruby                            | `rails`, `sinatra`                                 |
| `cargo`                    | Rust                            | `serde`, `tokio`                                   |
| `composer`                 | PHP                             | `laravel/framework`                                |
| `cocoapods` / `pods`        | Swift / Objective‑C             | `Alamofire`                                        |
| `conan`                    | C / C++                         | `boost`, `openssl`                                 |
| `debian` / `deb`            | Debian / Ubuntu                 | `nginx`, `curl`                                    |
| `rpm` / `yum`               | RHEL / CentOS / Fedora          | `httpd`, `openssl`                                 |
| `alpine`                   | Alpine Linux                    | `curl`, `git`                                      |
| `helm`                     | Kubernetes                      | `ingress-nginx`                                    |
| _(anything else)_           | Generic fallback                | Falls back to storage API paths                    |
