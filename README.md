# JFrog Artifactory Package Checker

## Installation

### 1) Clone from repository

    git clone https://github.com/H3llKa1ser/jfrog-artifactory-package-checker

Then

    cd jfrog-artifactory-package-checker/

### 2) Compile into an executable

Linux

    go build -o jfrog_package_checker jfrog_package_checker.go

Windows 64-bit

    GOOS=windows GOARCH=amd64 go build -o jfrog_package_checker.exe jfrog_package_checker.go

OR you can run build.sh to compile the app for Windows, Linux and MacOS

    chmod +x build.sh
    ./build.sh

### 3) Give the file executable permissions

    chmod +x jfrog_package_checker

### 4) Move the file to a location to execute system wide

    sudo cp jfrog_package_checker /usr/bin/jfrog_package_checker

## Usage

### 1) Run the tool with your generated JFrog credentials

    ./jfrog_package_checker -csv PACKAGE_LIST.csv -host https://artifactory.mycompany.com -user USER -pass PASSWORD -output ASSESSED_PACKAGE_LIST.csv

### 2) Run parallel HTTP requests for faster assessment time (be careful to not overload the instance if it might not handle many concurrent requests!)

Number can be from 10 (Slow) to 200+ (Fast). Default is 50.

    ./jfrog_package_checker -csv PACKAGE_LIST.csv -host https://artifactory.mycompany.com -user USER -pass PASSWORD -output ASSESSED_PACKAGE_LIST.csv -workers 100

### 3) Help menu

    ./jfrog_package_checker -help

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

You can use the package_list.csv file as a reference.
