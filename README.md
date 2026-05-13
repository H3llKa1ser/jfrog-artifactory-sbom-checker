# JFrog Artifactory Package Checker

## Installation

### 1) Clone from repository

    git clone https://github.com/H3llKa1ser/jfrog-artifactory-package-checker

### 2) Build go file into an executable

    go build -o check_packages check_packages.go

### 3) Give the file executable permissions

    chmod +x check_packages

### 4) Move the file to a location to execute system wide

    mv check_packages /usr/bin/check_packages

## Usage

### 1) Run the tool with basic authentication

    ./check_packages -csv PACKAGE_LIST.csv -host https://artifactory.mycompany.com -user USER -pass PASSWORD -output ASSESSED_PACKAGE_LIST.csv

### 2) Run parallel HTTP requests for faster assessment time (be careful on not overloading the instance!)

Number can be from 10 (Slow) to 200+ (Fast)

    ./check_packages -csv PACKAGE_LIST.csv -host https://artifactory.mycompany.com -user USER -pass PASSWORD -output ASSESSED_PACKAGE_LIST.csv -workers 100
