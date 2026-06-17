// pkg-check.go
//
// jfrog-pkg-checker: Verifies the existence of packages across ALL repositories
// in a JFrog Artifactory instance, for multiple ecosystems (pypi, npm, maven,
// composer, docker, go, nuget, gem). Prompts interactively for credentials
// (password hidden), accepts .csv or .txt input, and exports results to .xlsx.
//
// Dependencies (run before building):
//   go mod init jfrog-pkg-checker
//   go get github.com/xuri/excelize/v2
//   go get golang.org/x/term
//
// Build:
//   go build -o pkg-check pkg-check.go
//
// Input formats:
//   .txt -> concatenated:  <name>@<version>XRAY-<id>...
//   .csv -> columns:       name,version,xray_id   (header optional)

package main

import (
    "bufio"
    "encoding/csv"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "regexp"
    "strings"
    "syscall"
    "time"

    "github.com/xuri/excelize/v2"
    "golang.org/x/term"
)

var ecosystemAliases = map[string]string{
    "python":    "pypi",
    "pip":       "pypi",
    "pypi":      "pypi",
    "node":      "npm",
    "nodejs":    "npm",
    "npm":       "npm",
    "java":      "maven",
    "maven":     "maven",
    "gradle":    "maven",
    "php":       "composer",
    "composer":  "composer",
    "docker":    "docker",
    "container": "docker",
    "go":        "go",
    "golang":    "go",
    "nuget":     "nuget",
    "dotnet":    "nuget",
    "gem":       "gem",
    "ruby":      "gem",
    "rubygems":  "gem",
}

func resolveEcosystem(input string) (string, bool) {
    canon, ok := ecosystemAliases[strings.ToLower(strings.TrimSpace(input))]
    return canon, ok
}

type Package struct {
    Name    string
    Version string
    XrayID  string
}

type Repository struct {
    Key         string `json:"key"`
    Type        string `json:"type"`
    PackageType string `json:"packageType"`
    URL         string `json:"url"`
}

type Result struct {
    Package   Package
    Exists    bool
    FoundIn   []string
    Checked   int
    Error     string
    CheckedAt string
}

type Client struct {
    BaseURL  string
    Username string
    Password string
    HTTP     *http.Client
}

func NewClient(baseURL, username, password string) *Client {
    return &Client{
        BaseURL:  strings.TrimRight(baseURL, "/"),
        Username: username,
        Password: password,
        HTTP:     &http.Client{Timeout: 30 * time.Second},
    }
}

func (c *Client) auth(req *http.Request) {
    req.SetBasicAuth(c.Username, c.Password)
    req.Header.Set("Accept", "application/json")
}

func (c *Client) ListRepositories(packageType string) ([]Repository, error) {
    url := c.BaseURL + "/api/repositories"
    req, err := http.NewRequest(http.MethodGet, url, nil)
    if err != nil {
        return nil, fmt.Errorf("building repositories request: %w", err)
    }
    c.auth(req)

    resp, err := c.HTTP.Do(req)
    if err != nil {
        return nil, fmt.Errorf("listing repositories: %w", err)
    }
    defer resp.Body.Close()

    body, _ := io.ReadAll(resp.Body)
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("listing repositories: unexpected status %d: %s",
            resp.StatusCode, strings.TrimSpace(string(body)))
    }

    var repos []Repository
    if err := json.Unmarshal(body, &repos); err != nil {
        return nil, fmt.Errorf("parsing repositories response: %w", err)
    }

    if packageType == "" {
        return repos, nil
    }

    filtered := make([]Repository, 0, len(repos))
    for _, r := range repos {
        if strings.EqualFold(r.PackageType, packageType) {
            filtered = append(filtered, r)
        }
    }
    return filtered, nil
}

var recordRe = regexp.MustCompile(`(@?[A-Za-z0-9._\-\/]+?)@([0-9][A-Za-z0-9.\-+]*)(XRAY-\d+)`)

// parseTxt parses the concatenated .txt format.
func parseTxt(raw string) []Package {
    cleaned := strings.NewReplacer(
        "\n", "", "\r", "", "\t", "", " ", "", "**", "",
    ).Replace(raw)

    matches := recordRe.FindAllStringSubmatch(cleaned, -1)
    pkgs := make([]Package, 0, len(matches))
    for _, m := range matches {
        pkgs = append(pkgs, Package{Name: m[1], Version: m[2], XrayID: m[3]})
    }
    return pkgs
}

// parseCSV parses a .csv file. Supports an optional header row containing
// "name", "version", "xray_id" (in any order). If no recognizable header is
// found, it assumes columns are in the order: name, version, xray_id.
func parseCSV(f io.Reader) ([]Package, error) {
    r := csv.NewReader(f)
    r.FieldsPerRecord = -1 // allow variable field counts
    r.TrimLeadingSpace = true

    rows, err := r.ReadAll()
    if err != nil {
        return nil, fmt.Errorf("reading csv: %w", err)
    }
    if len(rows) == 0 {
        return nil, nil
    }

    // Default column positions.
    nameIdx, verIdx, xrayIdx := 0, 1, 2
    startRow := 0

    // Detect a header row.
    header := rows[0]
    isHeader := false
    for i, col := range header {
        switch strings.ToLower(strings.TrimSpace(col)) {
        case "name", "package", "package_name":
            nameIdx, isHeader = i, true
        case "version", "ver":
            verIdx, isHeader = i, true
        case "xray_id", "xray", "xrayid", "id":
            xrayIdx, isHeader = i, true
        }
    }
    if isHeader {
        startRow = 1
    }

    pkgs := make([]Package, 0, len(rows))
    for _, row := range rows[startRow:] {
        if len(row) == 0 {
            continue
        }
        var p Package

        // Some CSV exports put "name@version" in a single cell — handle that.
        first := strings.TrimSpace(row[nameIdx])
        if (len(row) <= verIdx) && strings.Contains(first, "@") {
            at := strings.LastIndex(first, "@")
            p.Name = first[:at]
            p.Version = first[at+1:]
        } else {
            p.Name = first
            if verIdx < len(row) {
                p.Version = strings.TrimSpace(row[verIdx])
            }
        }
        if xrayIdx < len(row) {
            p.XrayID = strings.TrimSpace(row[xrayIdx])
        }

        if p.Name == "" {
            continue
        }
        pkgs = append(pkgs, p)
    }
    return pkgs, nil
}

// loadPackages reads the input file and parses it based on its extension.
func loadPackages(path string) ([]Package, error) {
    ext := strings.ToLower(filepath.Ext(path))
    switch ext {
    case ".csv":
        f, err := os.Open(path)
        if err != nil {
            return nil, fmt.Errorf("opening %q: %w", path, err)
        }
        defer f.Close()
        return parseCSV(f)

    case ".txt":
        data, err := os.ReadFile(path)
        if err != nil {
            return nil, fmt.Errorf("reading %q: %w", path, err)
        }
        return parseTxt(string(data)), nil

    default:
        return nil, fmt.Errorf("unsupported input extension %q (use .csv or .txt)", ext)
    }
}

func artifactPath(ecosystem, name, version string) string {
    switch ecosystem {
    case "npm":
        base := name
        if idx := strings.LastIndex(name, "/"); idx != -1 {
            base = name[idx+1:]
        }
        return fmt.Sprintf("%s/-/%s-%s.tgz", name, base, version)
    case "pypi":
        return fmt.Sprintf("%s/%s-%s.tar.gz", name, name, version)
    case "maven":
        g, a := name, name
        sep := ""
        if strings.Contains(name, ":") {
            sep = ":"
        } else if strings.Contains(name, "/") {
            sep = "/"
        }
        if sep != "" {
            idx := strings.LastIndex(name, sep)
            g = name[:idx]
            a = name[idx+1:]
        }
        groupPath := strings.ReplaceAll(strings.ReplaceAll(g, ".", "/"), ":", "/")
        return fmt.Sprintf("%s/%s/%s/%s-%s.jar", groupPath, a, version, a, version)
    case "composer":
        return fmt.Sprintf("%s/%s.zip", name, version)
    case "docker":
        return fmt.Sprintf("%s/%s/manifest.json", name, version)
    case "go":
        return fmt.Sprintf("%s/@v/%s.zip", name, version)
    case "nuget":
        return fmt.Sprintf("%s.%s.nupkg", strings.ToLower(name), version)
    case "gem":
        return fmt.Sprintf("gems/%s-%s.gem", name, version)
    default:
        return fmt.Sprintf("%s/%s", name, version)
    }
}

func (c *Client) existsInRepo(repoKey, ecosystem string, p Package) (bool, error) {
    path := artifactPath(ecosystem, p.Name, p.Version)
    url := fmt.Sprintf("%s/api/storage/%s/%s", c.BaseURL, repoKey, path)

    req, err := http.NewRequest(http.MethodGet, url, nil)
    if err != nil {
        return false, fmt.Errorf("building request: %w", err)
    }
    c.auth(req)

    resp, err := c.HTTP.Do(req)
    if err != nil {
        return false, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()
    _, _ = io.Copy(io.Discard, resp.Body)

    switch resp.StatusCode {
    case http.StatusOK:
        return true, nil
    case http.StatusNotFound:
        return false, nil
    case http.StatusUnauthorized, http.StatusForbidden:
        return false, fmt.Errorf("auth/permission denied for repo %q", repoKey)
    default:
        return false, fmt.Errorf("unexpected status %d for repo %q", resp.StatusCode, repoKey)
    }
}

func (c *Client) CheckPackage(p Package, ecosystem string, repos []Repository) Result {
    res := Result{
        Package:   p,
        FoundIn:   []string{},
        CheckedAt: time.Now().UTC().Format(time.RFC3339),
    }
    var errs []string
    for _, repo := range repos {
        res.Checked++
        found, err := c.existsInRepo(repo.Key, ecosystem, p)
        if err != nil {
            errs = append(errs, err.Error())
            continue
        }
        if found {
            res.Exists = true
            res.FoundIn = append(res.FoundIn, repo.Key)
        }
    }
    if len(errs) > 0 {
        res.Error = strings.Join(errs, "; ")
    }
    return res
}

func prompt(reader *bufio.Reader, label string) string {
    fmt.Print(label)
    line, _ := reader.ReadString('\n')
    return strings.TrimSpace(line)
}

func promptPassword(label string) (string, error) {
    fmt.Print(label)
    bytePwd, err := term.ReadPassword(int(syscall.Stdin))
    fmt.Println()
    if err != nil {
        return "", fmt.Errorf("reading password: %w", err)
    }
    return strings.TrimSpace(string(bytePwd)), nil
}

func writeXLSX(path, instance, ecosystem string, repoKeys []string, results []Result) error {
    f := excelize.NewFile()
    defer f.Close()

    const sheet = "Results"
    f.SetSheetName("Sheet1", sheet)

    headers := []string{"Name", "Version", "XRAY ID", "Exists", "Found In", "Repos Checked", "Error", "Checked At"}
    for i, h := range headers {
        cell, _ := excelize.CoordinatesToCellName(i+1, 1)
        _ = f.SetCellValue(sheet, cell, h)
    }
    style, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
    _ = f.SetRowStyle(sheet, 1, 1, style)

    for r, res := range results {
        row := r + 2
        values := []interface{}{
            res.Package.Name,
            res.Package.Version,
            res.Package.XrayID,
            res.Exists,
            strings.Join(res.FoundIn, ", "),
            res.Checked,
            res.Error,
            res.CheckedAt,
        }
        for c, v := range values {
            cell, _ := excelize.CoordinatesToCellName(c+1, row)
            _ = f.SetCellValue(sheet, cell, v)
        }
    }

    const summary = "Summary"
    _, _ = f.NewSheet(summary)
    found, missing, errCount := 0, 0, 0
    for _, res := range results {
        switch {
        case res.Exists:
            found++
        case res.Error != "":
            errCount++
        default:
            missing++
        }
    }
    summaryRows := [][]interface{}{
        {"Instance", instance},
        {"Ecosystem", ecosystem},
        {"Repositories checked", strings.Join(repoKeys, ", ")},
        {"Total packages", len(results)},
        {"Found", found},
        {"Missing", missing},
        {"Errors", errCount},
        {"Generated at", time.Now().UTC().Format(time.RFC3339)},
    }
    for i, sr := range summaryRows {
        c1, _ := excelize.CoordinatesToCellName(1, i+1)
        c2, _ := excelize.CoordinatesToCellName(2, i+1)
        _ = f.SetCellValue(summary, c1, sr[0])
        _ = f.SetCellValue(summary, c2, sr[1])
    }

    return f.SaveAs(path)
}

func main() {
    reader := bufio.NewReader(os.Stdin)

    baseURL := prompt(reader, "Artifactory URL: ")
    if baseURL == "" {
        fmt.Fprintln(os.Stderr, "error: URL is required")
        os.Exit(2)
    }

    username := prompt(reader, "Username: ")
    if username == "" {
        fmt.Fprintln(os.Stderr, "error: username is required")
        os.Exit(2)
    }

    password, err := promptPassword("Password / API key: ")
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(2)
    }
    if password == "" {
        fmt.Fprintln(os.Stderr, "error: password is required")
        os.Exit(2)
    }

    ecoInput := prompt(reader, "Ecosystem (e.g. python, node, java, php, docker, go, nuget, ruby): ")
    ecosystem, ok := resolveEcosystem(ecoInput)
    if !ok {
        fmt.Fprintf(os.Stderr, "error: unknown ecosystem %q\n", ecoInput)
        os.Exit(2)
    }

    inputFile := prompt(reader, "Input file path (.csv or .txt) [packages.txt]: ")
    if inputFile == "" {
        inputFile = "packages.txt"
    }

    outFile := prompt(reader, "Output .xlsx file [report.xlsx]: ")
    if outFile == "" {
        outFile = "report.xlsx"
    }

    pkgs, err := loadPackages(inputFile)
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(1)
    }
    if len(pkgs) == 0 {
        fmt.Fprintln(os.Stderr, "error: no packages parsed from input")
        os.Exit(1)
    }

    client := NewClient(baseURL, username, password)
    repos, err := client.ListRepositories(ecosystem)
    if err != nil {
        fmt.Fprintf(os.Stderr, "error discovering repositories: %v\n", err)
        os.Exit(1)
    }
    if len(repos) == 0 {
        fmt.Fprintf(os.Stderr, "error: no %s repositories found\n", ecosystem)
        os.Exit(1)
    }

    repoKeys := make([]string, 0, len(repos))
    for _, r := range repos {
        repoKeys = append(repoKeys, r.Key)
    }

    fmt.Printf("\nParsed %d package(s). Ecosystem: %s\n", len(pkgs), ecosystem)
    fmt.Printf("Discovered %d repository(ies): %s\n\n", len(repos), strings.Join(repoKeys, ", "))

    results := make([]Result, 0, len(pkgs))
    found, missing, errCount := 0, 0, 0
    for i, p := range pkgs {
        res := client.CheckPackage(p, ecosystem, repos)
        results = append(results, res)

        status := "MISSING"
        switch {
        case res.Exists:
            found++
            status = "FOUND"
        case res.Error != "":
            errCount++
            status = "ERROR"
        default:
            missing++
        }

        fmt.Printf("[%d/%d] %-7s %s@%s (%s)", i+1, len(pkgs), status, p.Name, p.Version, p.XrayID)
        if res.Exists {
            fmt.Printf(" -> %s", strings.Join(res.FoundIn, ", "))
        }
        fmt.Println()
    }

    fmt.Printf("\nSummary: %d total | %d found | %d missing | %d errors\n",
        len(pkgs), found, missing, errCount)

    if err := writeXLSX(outFile, client.BaseURL, ecosystem, repoKeys, results); err != nil {
        fmt.Fprintf(os.Stderr, "error writing xlsx: %v\n", err)
        os.Exit(1)
    }
    fmt.Printf("Report written to %s\n", outFile)

    if missing > 0 || errCount > 0 {
        os.Exit(1)
    }
}
