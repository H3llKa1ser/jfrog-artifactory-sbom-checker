///////////////////////////////////////////////////////////////////////////////
//  check_packages.go
//
//  Reads a CSV file of packages and checks whether each one exists
//  across ALL repositories in your private on-premises JFrog
//  Artifactory instance using concurrent goroutines with connection pooling.
//
//  BUILD:
//    go build -o check_packages check_packages.go
//
//  USAGE:
//    ./check_packages -csv packages.csv
//    ./check_packages -csv packages.csv -host https://artifactory.mycompany.com -user admin -pass secret
//    ./check_packages -csv packages.csv -token your-access-token
//    ./check_packages -csv packages.csv -output results.csv -workers 100
//
//  REQUIREMENTS: Go 1.22.2 linux/amd64
///////////////////////////////////////////////////////////////////////////////

package main

import (
    "crypto/tls"
    "encoding/csv"
    "encoding/json"
    "flag"
    "fmt"
    "io"
    "net/http"
    "os"
    "strings"
    "sync"
    "sync/atomic"
    "time"
)

// ========================== CONFIGURATION ====================================

const (
    defaultArtifactoryHost = "https://artifactory.mycompany.com"
    defaultUsername         = ""
    defaultPassword        = ""
    defaultToken           = ""
    defaultOutputFile      = "results.csv"
    defaultMaxWorkers      = 50
    defaultTimeout         = 10
    defaultSkipHeader      = true
    defaultInsecure        = false
)

// =============================================================================

// Colors for terminal output
const (
    colorRed    = "\033[0;31m"
    colorGreen  = "\033[0;32m"
    colorYellow = "\033[1;33m"
    colorCyan   = "\033[0;36m"
    colorBold   = "\033[1m"
    colorReset  = "\033[0m"
)

// ArtifactoryRepo represents a repository from the Artifactory API
type ArtifactoryRepo struct {
    Key         string `json:"key"`
    Type        string `json:"type"`
    PackageType string `json:"packageType"`
}

// Package represents a single package entry from the CSV
type Package struct {
    Index     int
    Ecosystem string
    Namespace string
    Name      string
    Version   string
}

// PackageResult holds the check result for a single package
type PackageResult struct {
    Package      Package
    Status       string
    FoundInRepos []string
}

// RepoCheckJob represents a single repo check task
type RepoCheckJob struct {
    Package Package
    Repo    string
}

// RepoCheckResult represents the result of checking one repo
type RepoCheckResult struct {
    PackageIndex int
    Repo         string
    Found        bool
    AuthError    bool
}

// Config holds all runtime configuration
type Config struct {
    ArtifactoryHost string
    Username        string
    Password        string
    Token           string
    CSVFile         string
    OutputFile      string
    MaxWorkers      int
    Timeout         int
    SkipHeader      bool
    Insecure        bool
}

func main() {
    // ---- Parse command-line flags ----
    config := parseFlags()

    // ---- Validate ----
    if config.CSVFile == "" {
        fmt.Printf("%sERROR: No CSV file provided.%s\n", colorRed, colorReset)
        fmt.Println("Usage: ./check_packages -csv /path/to/packages.csv")
        flag.PrintDefaults()
        os.Exit(1)
    }

    if _, err := os.Stat(config.CSVFile); os.IsNotExist(err) {
        fmt.Printf("%sERROR: File '%s' not found.%s\n", colorRed, config.CSVFile, colorReset)
        os.Exit(1)
    }

    // Check auth
    if config.Token == "" && (config.Username == "" || config.Password == "") {
        // Check environment variables as fallback
        if envToken := os.Getenv("ARTIFACTORY_TOKEN"); envToken != "" {
            config.Token = envToken
        } else if envUser := os.Getenv("ARTIFACTORY_USER"); envUser != "" {
            config.Username = envUser
            config.Password = os.Getenv("ARTIFACTORY_PASSWORD")
        }
    }

    if config.ArtifactoryHost == defaultArtifactoryHost || config.ArtifactoryHost == "" {
        if envHost := os.Getenv("ARTIFACTORY_HOST"); envHost != "" {
            config.ArtifactoryHost = envHost
        }
    }

    // ---- Create HTTP client with connection pooling ----
    httpClient := createHTTPClient(config)

    // ---- Print banner ----
    printBanner(config)

    // ---- Step 1: Fetch all repositories ----
    repos, err := fetchAllRepos(httpClient, config)
    if err != nil {
        fmt.Printf("%sERROR: Failed to fetch repositories: %v%s\n", colorRed, err, colorReset)
        os.Exit(1)
    }

    if len(repos) == 0 {
        fmt.Printf("%sERROR: No repositories found. Check your credentials and permissions.%s\n", colorRed, colorReset)
        os.Exit(1)
    }

    fmt.Printf("  %sTotal repositories found: %d%s\n\n", colorGreen, len(repos), colorReset)

    // ---- Step 2: Read packages from CSV ----
    packages, err := readCSV(config)
    if err != nil {
        fmt.Printf("%sERROR: Failed to read CSV: %v%s\n", colorRed, err, colorReset)
        os.Exit(1)
    }

    if len(packages) == 0 {
        fmt.Printf("%sERROR: No packages found in CSV file.%s\n", colorRed, colorReset)
        os.Exit(1)
    }

    fmt.Printf("  %sTotal packages to check: %d%s\n", colorCyan, len(packages), colorReset)
    fmt.Printf("  %sTotal checks: %d packages × %d repos × 3 URL patterns = ~%d requests%s\n",
        colorCyan, len(packages), len(repos), len(packages)*len(repos)*3, colorReset)
    fmt.Println()

    // ---- Step 3: Check all packages across all repos in parallel ----
    fmt.Printf("%s[STEP 2] Checking packages across all %d repositories (parallel)...%s\n\n",
        colorCyan, len(repos), colorReset)

    startTime := time.Now()
    results := checkAllPackages(httpClient, config, packages, repos)
    elapsed := time.Since(startTime)

    // ---- Step 4: Write results ----
    fmt.Printf("\n%s[STEP 3] Writing results...%s\n", colorCyan, colorReset)
    err = writeResults(config, results)
    if err != nil {
        fmt.Printf("%sERROR: Failed to write results: %v%s\n", colorRed, err, colorReset)
        os.Exit(1)
    }

    // ---- Step 5: Print summary ----
    printSummary(config, results, repos, elapsed)
}

// ---- Parse command-line flags ----
func parseFlags() Config {
    config := Config{}

    flag.StringVar(&config.ArtifactoryHost, "host", defaultArtifactoryHost, "Artifactory base URL")
    flag.StringVar(&config.Username, "user", defaultUsername, "Artifactory username")
    flag.StringVar(&config.Password, "pass", defaultPassword, "Artifactory password")
    flag.StringVar(&config.Token, "token", defaultToken, "Artifactory access token (alternative to user/pass)")
    flag.StringVar(&config.CSVFile, "csv", "", "Path to input CSV file (required)")
    flag.StringVar(&config.OutputFile, "output", defaultOutputFile, "Path to output CSV file")
    flag.IntVar(&config.MaxWorkers, "workers", defaultMaxWorkers, "Max concurrent HTTP requests")
    flag.IntVar(&config.Timeout, "timeout", defaultTimeout, "HTTP request timeout in seconds")
    flag.BoolVar(&config.SkipHeader, "skip-header", defaultSkipHeader, "Skip first line of CSV (header)")
    flag.BoolVar(&config.Insecure, "insecure", defaultInsecure, "Skip TLS certificate verification")

    flag.Parse()
    return config
}

// ---- Create HTTP client with connection pooling ----
func createHTTPClient(config Config) *http.Client {
    transport := &http.Transport{
        MaxIdleConns:        config.MaxWorkers + 10,
        MaxIdleConnsPerHost: config.MaxWorkers + 10,
        IdleConnTimeout:     90 * time.Second,
        DisableKeepAlives:   false,
        TLSClientConfig: &tls.Config{
            InsecureSkipVerify: config.Insecure,
        },
    }

    return &http.Client{
        Timeout:   time.Duration(config.Timeout) * time.Second,
        Transport: transport,
        // Don't follow redirects — we only care about the status code
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            return http.ErrUseLastResponse
        },
    }
}

// ---- Build authenticated HTTP request ----
func newAuthenticatedRequest(config Config, method string, url string) (*http.Request, error) {
    req, err := http.NewRequest(method, url, nil)
    if err != nil {
        return nil, err
    }

    if config.Token != "" {
        req.Header.Set("Authorization", "Bearer "+config.Token)
    } else if config.Username != "" {
        req.SetBasicAuth(config.Username, config.Password)
    }

    return req, nil
}

// ---- Print banner ----
func printBanner(config Config) {
    fmt.Println()
    fmt.Println("============================================================")
    fmt.Println("  JFrog Artifactory — Go Parallel Package Checker")
    fmt.Println("============================================================")
    fmt.Printf("  Artifactory : %s\n", config.ArtifactoryHost)
    fmt.Printf("  CSV File    : %s\n", config.CSVFile)
    fmt.Printf("  Output File : %s\n", config.OutputFile)
    fmt.Printf("  Workers     : %d concurrent\n", config.MaxWorkers)
    fmt.Printf("  Timeout     : %ds\n", config.Timeout)
    fmt.Printf("  Insecure    : %v\n", config.Insecure)
    fmt.Println("============================================================")
    fmt.Println()
    fmt.Printf("%s[STEP 1] Fetching all repositories...%s\n", colorCyan, colorReset)
}

///////////////////////////////////////////////////////////////////////////////
//  STEP 1: FETCH ALL REPOSITORIES
///////////////////////////////////////////////////////////////////////////////

func fetchAllRepos(client *http.Client, config Config) ([]string, error) {
    url := config.ArtifactoryHost + "/artifactory/api/repositories"

    req, err := newAuthenticatedRequest(config, "GET", url)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    resp, err := client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
    }

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("failed to read response: %w", err)
    }

    var repos []ArtifactoryRepo
    if err := json.Unmarshal(body, &repos); err != nil {
        return nil, fmt.Errorf("failed to parse JSON: %w", err)
    }

    repoKeys := make([]string, 0, len(repos))
    for _, r := range repos {
        repoKeys = append(repoKeys, r.Key)
    }

    return repoKeys, nil
}

///////////////////////////////////////////////////////////////////////////////
//  STEP 2: READ PACKAGES FROM CSV
///////////////////////////////////////////////////////////////////////////////

func readCSV(config Config) ([]Package, error) {
    file, err := os.Open(config.CSVFile)
    if err != nil {
        return nil, err
    }
    defer file.Close()

    reader := csv.NewReader(file)
    reader.FieldsPerRecord = -1 // Allow variable number of fields
    reader.LazyQuotes = true
    reader.TrimLeadingSpace = true

    var packages []Package
    lineNum := 0
    index := 0

    for {
        record, err := reader.Read()
        if err == io.EOF {
            break
        }
        if err != nil {
            // Skip malformed lines
            lineNum++
            continue
        }

        lineNum++

        // Skip header
        if config.SkipHeader && lineNum == 1 {
            continue
        }

        // Need at least 4 fields: ecosystem, namespace, name, version
        if len(record) < 4 {
            continue
        }

        ecosystem := strings.TrimSpace(record[0])
        namespace := strings.TrimSpace(record[1])
        name := strings.TrimSpace(record[2])
        version := strings.TrimSpace(record[3])

        // Skip empty entries
        if name == "" || version == "" {
            continue
        }

        index++
        packages = append(packages, Package{
            Index:     index,
            Ecosystem: ecosystem,
            Namespace: namespace,
            Name:      name,
            Version:   version,
        })
    }

    return packages, nil
}

///////////////////////////////////////////////////////////////////////////////
//  STEP 3: BUILD URLS FOR A PACKAGE IN A GIVEN REPO
///////////////////////////////////////////////////////////////////////////////

func buildURLs(config Config, repo string, pkg Package) []string {
    base := config.ArtifactoryHost + "/artifactory"
    var urls []string

    if pkg.Namespace != "" {
        // Scoped package: @scope/name
        urls = append(urls,
            fmt.Sprintf("%s/api/npm/%s/%s/%s/%s", base, repo, pkg.Namespace, pkg.Name, pkg.Version),
            fmt.Sprintf("%s/api/storage/%s/%s/%s/-/%s-%s.tgz", base, repo, pkg.Namespace, pkg.Name, pkg.Name, pkg.Version),
            fmt.Sprintf("%s/api/storage/%s/%s/%s/%s", base, repo, pkg.Namespace, pkg.Name, pkg.Version),
        )
    } else {
        // Unscoped package
        urls = append(urls,
            fmt.Sprintf("%s/api/npm/%s/%s/%s", base, repo, pkg.Name, pkg.Version),
            fmt.Sprintf("%s/api/storage/%s/%s/-/%s-%s.tgz", base, repo, pkg.Name, pkg.Name, pkg.Version),
            fmt.Sprintf("%s/api/storage/%s/%s/%s", base, repo, pkg.Name, pkg.Version),
        )
    }

    return urls
}

///////////////////////////////////////////////////////////////////////////////
//  STEP 4: CHECK ALL PACKAGES IN PARALLEL
///////////////////////////////////////////////////////////////////////////////

func checkAllPackages(client *http.Client, config Config, packages []Package, repos []string) []PackageResult {
    // ---- Create a buffered channel for jobs ----
    jobs := make(chan RepoCheckJob, config.MaxWorkers*2)

    // ---- Create a channel for results ----
    repoResults := make(chan RepoCheckResult, config.MaxWorkers*2)

    // ---- Per-package result tracking ----
    type packageTracker struct {
        mu           sync.Mutex
        foundRepos   []string
        authError    bool
        pendingCount int32
    }

    trackers := make(map[int]*packageTracker)
    for _, pkg := range packages {
        trackers[pkg.Index] = &packageTracker{
            pendingCount: int32(len(repos)),
        }
    }

    // ---- Progress counters (atomic) ----
    var completedPackages atomic.Int32

    // ---- Start worker goroutines ----
    var workerWg sync.WaitGroup
    for i := 0; i < config.MaxWorkers; i++ {
        workerWg.Add(1)
        go func() {
            defer workerWg.Done()
            for job := range jobs {
                found, authErr := checkRepoForPackage(client, config, job)
                repoResults <- RepoCheckResult{
                    PackageIndex: job.Package.Index,
                    Repo:         job.Repo,
                    Found:        found,
                    AuthError:    authErr,
                }
            }
        }()
    }

    // ---- Start result collector goroutine ----
    var collectorWg sync.WaitGroup
    collectorWg.Add(1)

    // Channel to signal when a package is fully done
    packageDone := make(chan PackageResult, len(packages))

    go func() {
        defer collectorWg.Done()
        for result := range repoResults {
            tracker := trackers[result.PackageIndex]
            tracker.mu.Lock()

            if result.Found {
                tracker.foundRepos = append(tracker.foundRepos, result.Repo)
            }
            if result.AuthError {
                tracker.authError = true
            }

            remaining := atomic.AddInt32(&tracker.pendingCount, -1)

            if remaining == 0 {
                // This package is fully checked — build final result
                var pkg Package
                for _, p := range packages {
                    if p.Index == result.PackageIndex {
                        pkg = p
                        break
                    }
                }

                status := "NOT_FOUND"
                if len(tracker.foundRepos) > 0 {
                    status = "FOUND"
                } else if tracker.authError {
                    status = "AUTH_FAILED"
                }

                pr := PackageResult{
                    Package:      pkg,
                    Status:       status,
                    FoundInRepos: tracker.foundRepos,
                }

                // Print result
                printPackageResult(pr, len(repos))
                completedPackages.Add(1)

                packageDone <- pr
            }

            tracker.mu.Unlock()
        }
    }()

    // ---- Feed jobs into the worker pool ----
    go func() {
        for _, pkg := range packages {
            for _, repo := range repos {
                jobs <- RepoCheckJob{
                    Package: pkg,
                    Repo:    repo,
                }
            }
        }
        close(jobs)
    }()

    // ---- Wait for all workers to finish ----
    workerWg.Wait()
    close(repoResults)

    // ---- Wait for collector to finish ----
    collectorWg.Wait()
    close(packageDone)

    // ---- Collect all results in order ----
    resultMap := make(map[int]PackageResult)
    for pr := range packageDone {
        resultMap[pr.Package.Index] = pr
    }

    // Build ordered results
    results := make([]PackageResult, 0, len(packages))
    for _, pkg := range packages {
        if r, ok := resultMap[pkg.Index]; ok {
            results = append(results, r)
        }
    }

    return results
}

// ---- Check a single repo for a package (tries multiple URL patterns) ----
func checkRepoForPackage(client *http.Client, config Config, job RepoCheckJob) (found bool, authError bool) {
    urls := buildURLs(config, job.Repo, job.Package)

    for _, url := range urls {
        req, err := newAuthenticatedRequest(config, "HEAD", url)
        if err != nil {
            continue
        }

        resp, err := client.Do(req)
        if err != nil {
            continue
        }
        resp.Body.Close()

        switch resp.StatusCode {
        case http.StatusOK:
            return true, false
        case http.StatusUnauthorized, http.StatusForbidden:
            authError = true
        }
        // 404 or other — continue to next URL pattern
    }

    return false, authError
}

// ---- Print a single package result ----
func printPackageResult(result PackageResult, totalRepos int) {
    fullName := result.Package.Name
    if result.Package.Namespace != "" {
        fullName = result.Package.Namespace + "/" + result.Package.Name
    }

    switch result.Status {
    case "FOUND":
        repos := strings.Join(result.FoundInRepos, " | ")
        fmt.Printf("  [%d] %s%s@%s%s ... %s✅ FOUND (in: %s)%s\n",
            result.Package.Index, colorBold, fullName, result.Package.Version, colorReset,
            colorGreen, repos, colorReset)
    case "AUTH_FAILED":
        fmt.Printf("  [%d] %s%s@%s%s ... %s🔒 AUTH FAILED%s\n",
            result.Package.Index, colorBold, fullName, result.Package.Version, colorReset,
            colorYellow, colorReset)
    default:
        fmt.Printf("  [%d] %s%s@%s%s ... %s❌ NOT FOUND (checked %d repos)%s\n",
            result.Package.Index, colorBold, fullName, result.Package.Version, colorReset,
            colorRed, totalRepos, colorReset)
    }
}

///////////////////////////////////////////////////////////////////////////////
//  STEP 5: WRITE RESULTS TO CSV
///////////////////////////////////////////////////////////////////////////////

func writeResults(config Config, results []PackageResult) error {
    file, err := os.Create(config.OutputFile)
    if err != nil {
        return err
    }
    defer file.Close()

    writer := csv.NewWriter(file)
    defer writer.Flush()

    // Write header
    err = writer.Write([]string{"Ecosystem", "Namespace", "Name", "Version", "Status", "Found_In_Repos"})
    if err != nil {
        return err
    }

    for _, r := range results {
        repos := "N/A"
        if len(r.FoundInRepos) > 0 {
            repos = strings.Join(r.FoundInRepos, "|")
        }

        err = writer.Write([]string{
            r.Package.Ecosystem,
            r.Package.Namespace,
            r.Package.Name,
            r.Package.Version,
            r.Status,
            repos,
        })
        if err != nil {
            return err
        }
    }

    return nil
}

///////////////////////////////////////////////////////////////////////////////
//  STEP 6: PRINT SUMMARY
///////////////////////////////////////////////////////////////////////////////

func printSummary(config Config, results []PackageResult, repos []string, elapsed time.Duration) {
    found := 0
    notFound := 0
    errors := 0

    for _, r := range results {
        switch r.Status {
        case "FOUND":
            found++
        case "NOT_FOUND":
            notFound++
        default:
            errors++
        }
    }

    totalRequests := len(results) * len(repos) * 3

    fmt.Println()
    fmt.Println("============================================================")
    fmt.Println("  SUMMARY")
    fmt.Println("============================================================")
    fmt.Printf("  Repos Checked       : %s%d%s\n", colorCyan, len(repos), colorReset)
    fmt.Printf("  Total Packages      : %s%d%s\n", colorCyan, len(results), colorReset)
    fmt.Printf("  ✅ Found            : %s%d%s\n", colorGreen, found, colorReset)
    fmt.Printf("  ❌ Not Found        : %s%d%s\n", colorRed, notFound, colorReset)
    fmt.Printf("  ⚠️  Errors           : %s%d%s\n", colorYellow, errors, colorReset)
    fmt.Println("------------------------------------------------------------")
    fmt.Printf("  Workers             : %s%d concurrent%s\n", colorCyan, config.MaxWorkers, colorReset)
    fmt.Printf("  Total HTTP Requests : %s~%d%s\n", colorCyan, totalRequests, colorReset)
    fmt.Printf("  Time Elapsed        : %s%s%s\n", colorCyan, elapsed.Round(time.Millisecond), colorReset)
    fmt.Printf("  Requests/sec        : %s%.0f%s\n", colorCyan, float64(totalRequests)/elapsed.Seconds(), colorReset)
    fmt.Println("============================================================")
    fmt.Printf("  Results saved to: %s\n", config.OutputFile)
    fmt.Println("============================================================")
    fmt.Println()
}
