// jfrog_sbom_package_checker.go
//
// Verifies the existence of packages across ALL repositories in a JFrog
// Artifactory instance. The ecosystem is AUTO-DETECTED from keywords found
// inside the input file (.csv or .txt) using a hardcoded alias map.
// Prompts interactively for credentials (password hidden) and exports an .xlsx.
//
// Dependencies:
//   go mod init jfrog_sbom_package_checker
//   go get github.com/xuri/excelize/v2
//   go get golang.org/x/term
//   go mod tidy

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

// ecosystemAliases maps hardcoded keywords (including plurals) to canonical types.
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
    "containers": "docker",
    "go":        "go",
    "golang":    "go",
    "nuget":     "nuget",
    "dotnet":    "nuget",
    ".net":      "nuget",
    "gem":       "gem",
    "gems":      "gem",
    "ruby":      "gem",
    "rubygems":  "gem",
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
    Ecosystem string
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

func (c *Client) ListRepositories() ([]Repository, error) {
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
    return repos, nil
}

// reposForEcosystem returns repos whose packageType matches the canonical ecosystem.
func reposForEcosystem(all []Repository, ecosystem string) []Repository {
    out := make([]Repository, 0)
    for _, r := range all {
        if strings.EqualFold(r.PackageType, ecosystem) {
            out = append(out, r)
        }
    }
    return out
}

var recordRe = regexp.MustCompile(`(@?[A-Za-z0-9._\-\/]+?)@([0-9][A-Za-z0-9.\-+]*)(XRAY-\d+)`)

// detectEcosystems scans raw text for hardcoded ecosystem keywords (whole-word,
// case-insensitive) and returns the set of canonical ecosystems found, in a
// stable order.
func detectEcosystems(raw string) []string {
    lower := strings.ToLower(raw)
    seen := map[string]bool{}
    order := []string{}

    // Tokenize on any non-alphanumeric/.-+ character so keywords like "gems",
    // "ruby", ".net" are isolated from surrounding text.
    splitter := func(r rune) bool {
        switch {
        case r >= 'a' && r <= 'z':
            return false
        case r >= '0' && r <= '9':
            return false
        case r == '.' || r == '-' || r == '+':
            return false
        default:
            return true
        }
    }
    tokens := strings.FieldsFunc(lower, splitter)

    for _, t := range tokens {
        if canon, ok := ecosystemAliases[t]; ok {
            if !seen[canon] {
                seen[canon] = true
                order = append(order, canon)
            }
        }
    }
    return order
}

// stripEcosystemKeywords removes standalone ecosystem keyword tokens so they are
// not mistaken for package names during parsing.
func stripEcosystemKeywords(raw string) string {
    // Replace whole-word keyword occurrences (case-insensitive) with a space.
    // We sort keywords by length (desc) so longer ones (e.g. "rubygems") are
    // removed before shorter substrings.
    keywords := make([]string, 0, len(ecosystemAliases))
    for k := range ecosystemAliases {
        keywords = append(keywords, k)
    }
    // simple length-desc sort
    for i := 0; i < len(keywords); i++ {
        for j := i + 1; j < len(keywords); j++ {
            if len(keywords[j]) > len(keywords[i]) {
                keywords[i], keywords[j] = keywords[j], keywords[i]
            }
        }
    }

    result := raw
    for _, kw := range keywords {
        // \b word boundaries, case-insensitive. Escape regex-special chars (.net).
        pattern := `(?i)\b` + regexp.QuoteMeta(kw) + `\b`
        re := regexp.MustCompile(pattern)
        result = re.ReplaceAllString(result, " ")
    }
    return result
}

// parseTxt parses the concatenated .txt format AFTER keyword stripping.
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

// parseCSV parses a .csv file. Supports an optional header row (name, version,
// xray_id) or positional columns. Cells containing ecosystem keywords only are
// skipped. A "name@version" combined cell is also supported.
func parseCSV(f io.Reader) ([]Package, error) {
    r := csv.NewReader(f)
    r.FieldsPerRecord = -1
    r.TrimLeadingSpace = true

    rows, err := r.ReadAll()
    if err != nil {
        return nil, fmt.Errorf("reading csv: %w", err)
    }
    if len(rows) == 0 {
        return nil, nil
    }

    nameIdx, verIdx, xrayIdx := 0, 1, 2
    startRow := 0

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
        first := strings.TrimSpace(row[nameIdx])
        if first == "" {
            continue
        }
        // Skip cells that are purely an ecosystem keyword.
        if _, isKw := ecosystemAliases[strings.ToLower(first)]; isKw {
            continue
        }

        var p Package
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

// loadInput returns the raw file content and the parsed packages, based on ext.
func loadInput(path string) (raw string, pkgs []Package, err error) {
    ext := strings.ToLower(filepath.Ext(path))
    switch ext {
    case ".csv":
        data, e := os.ReadFile(path)
        if e != nil {
            return "", nil, fmt.Errorf("reading %q: %w", path, e)
        }
        raw = string(data)
        p, e := parseCSV(strings.NewReader(raw))
        if e != nil {
            return raw, nil, e
        }
        return raw, p, nil

    case ".txt":
        data, e := os.ReadFile(path)
        if e != nil {
            return "", nil, fmt.Errorf("reading %q: %w", path, e)
        }
        raw = string(data)
        stripped := stripEcosystemKeywords(raw)
        return raw, parseTxt(stripped), nil

    default:
        return "", nil, fmt.Errorf("unsupported input extension %q (use .csv or .txt)", ext)
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

// CheckPackage checks one package against the repos of a given ecosystem.
func (c *Client) CheckPackage(p Package, ecosystem string, repos []Repository) Result {
    res := Result{
        Package:   p,
        Ecosystem: ecosystem,
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

func writeXLSX(path, instance string, detected []string, repoKeys []string, results []Result) error {
    f := excelize.NewFile()
    defer f.Close()

    const sheet = "Results"
    f.SetSheetName("Sheet1", sheet)

    headers := []string{"Name", "Version", "XRAY ID", "Ecosystem", "Exists", "Found In", "Repos Checked", "Error", "Checked At"}
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
            res.Ecosystem,
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
        {"Detected ecosystems", strings.Join(detected, ", ")},
        {"Repositories checked", strings.Join(repoKeys, ", ")},
        {"Total checks", len(results)},
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

    inputFile := prompt(reader, "Input file path (.csv or .txt) [packages.txt]: ")
    if inputFile == "" {
        inputFile = "packages.txt"
    }

    outFile := prompt(reader, "Output .xlsx file [report.xlsx]: ")
    if outFile == "" {
        outFile = "report.xlsx"
    }

    // Load raw content + parsed packages.
    raw, pkgs, err := loadInput(inputFile)
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(1)
    }
    if len(pkgs) == 0 {
        fmt.Fprintln(os.Stderr, "error: no packages parsed from input")
        os.Exit(1)
    }

    // Auto-detect ecosystems from keywords inside the file.
    detected := detectEcosystems(raw)
    if len(detected) == 0 {
        fmt.Fprintln(os.Stderr, "error: no ecosystem keywords detected in the input file")
        fmt.Fprintln(os.Stderr, "       (expected keywords like: python, node, java, php, docker, go, nuget, ruby, gem...)")
        os.Exit(1)
    }

    client := NewClient(baseURL, username, password)

    allRepos, err := client.ListRepositories()
    if err != nil {
        fmt.Fprintf(os.Stderr, "error discovering repositories: %v\n", err)
        os.Exit(1)
    }

    fmt.Printf("\nParsed %d package(s).\n", len(pkgs))
    fmt.Printf("Detected ecosystem(s): %s\n", strings.Join(detected, ", "))

    // Build the set of repos to check (union across detected ecosystems).
    allRepoKeysSet := map[string]bool{}
    results := make([]Result, 0, len(pkgs)*len(detected))
    found, missing, errCount := 0, 0, 0

    for _, eco := range detected {
        repos := reposForEcosystem(allRepos, eco)
        if len(repos) == 0 {
            fmt.Printf("  (no %s repositories found in instance — skipping)\n", eco)
            continue
        }
        for _, r := range repos {
            allRepoKeysSet[r.Key] = true
        }

        fmt.Printf("\n== Ecosystem %q: %d repo(s) ==\n", eco, len(repos))
        for i, p := range pkgs {
            res := client.CheckPackage(p, eco, repos)
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

            fmt.Printf("[%s %d/%d] %-7s %s@%s (%s)", eco, i+1, len(pkgs), status, p.Name, p.Version, p.XrayID)
            if res.Exists {
                fmt.Printf(" -> %s", strings.Join(res.FoundIn, ", "))
            }
            fmt.Println()
        }
    }

    repoKeys := make([]string, 0, len(allRepoKeysSet))
    for k := range allRepoKeysSet {
        repoKeys = append(repoKeys, k)
    }

    fmt.Printf("\nSummary: %d checks | %d found | %d missing | %d errors\n",
        len(results), found, missing, errCount)

    if err := writeXLSX(outFile, client.BaseURL, detected, repoKeys, results); err != nil {
        fmt.Fprintf(os.Stderr, "error writing xlsx: %v\n", err)
        os.Exit(1)
    }
    fmt.Printf("Report written to %s\n", outFile)

    if missing > 0 || errCount > 0 {
        os.Exit(1)
    }
}
