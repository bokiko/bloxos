package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// MinerInfo contains info about a miner and how to install it
type MinerInfo struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Repo           string `json:"repo"`           // GitHub repo (owner/repo)
	AssetPattern   string `json:"assetPattern"`   // Pattern to match release asset
	BinaryName     string `json:"binaryName"`     // Name of the binary after extraction
	SupportedGPUs  string `json:"supportedGpus"`  // "nvidia", "amd", "both", "cpu"
	SupportedOS    string `json:"supportedOs"`    // "linux", "windows", "both"
}

// Available miners with their GitHub repos
var AvailableMiners = map[string]MinerInfo{
	"t-rex": {
		Name:          "T-Rex",
		Description:   "NVIDIA GPU miner for various algorithms",
		Repo:          "trexminer/T-Rex",
		AssetPattern:  "t-rex-%s-linux.tar.gz", // %s = version without 'v'
		BinaryName:    "t-rex",
		SupportedGPUs: "nvidia",
		SupportedOS:   "linux",
	},
	"lolminer": {
		Name:          "lolMiner",
		Description:   "AMD & NVIDIA GPU miner",
		Repo:          "Lolliedieb/lolMiner-releases",
		AssetPattern:  "lolMiner_%s_Lin64.tar.gz",
		BinaryName:    "lolMiner",
		SupportedGPUs: "both",
		SupportedOS:   "linux",
	},
	"gminer": {
		Name:          "GMiner",
		Description:   "High-performance miner for NVIDIA and AMD",
		Repo:          "develsoftware/GMinerRelease",
		AssetPattern:  "gminer_%s_linux64.tar.xz",
		BinaryName:    "miner",
		SupportedGPUs: "both",
		SupportedOS:   "linux",
	},
	"teamredminer": {
		Name:          "TeamRedMiner",
		Description:   "AMD GPU miner",
		Repo:          "todxx/teamredminer",
		AssetPattern:  "teamredminer-%s-linux.tar.gz",
		BinaryName:    "teamredminer",
		SupportedGPUs: "amd",
		SupportedOS:   "linux",
	},
	"xmrig": {
		Name:          "XMRig",
		Description:   "CPU/GPU miner for RandomX, KawPow, and more",
		Repo:          "xmrig/xmrig",
		AssetPattern:  "xmrig-%s-linux-x64.tar.gz",
		BinaryName:    "xmrig",
		SupportedGPUs: "cpu",
		SupportedOS:   "linux",
	},
	"nbminer": {
		Name:          "NBMiner",
		Description:   "NVIDIA & AMD GPU miner",
		Repo:          "NebuTech/NBMiner",
		AssetPattern:  "NBMiner_%s_Linux.tgz",
		BinaryName:    "nbminer",
		SupportedGPUs: "both",
		SupportedOS:   "linux",
	},
	"srbminer": {
		Name:          "SRBMiner-Multi",
		Description:   "CPU and AMD GPU miner",
		Repo:          "doktor83/SRBMiner-Multi",
		AssetPattern:  "SRBMiner-Multi-%s-Linux.tar.gz",
		BinaryName:    "SRBMiner-MULTI",
		SupportedGPUs: "amd",
		SupportedOS:   "linux",
	},
	"bzminer": {
		Name:          "BzMiner",
		Description:   "Multi-algorithm NVIDIA & AMD miner",
		Repo:          "bzminer/bzminer",
		AssetPattern:  "bzminer_%s_linux.tar.gz",
		BinaryName:    "bzminer",
		SupportedGPUs: "both",
		SupportedOS:   "linux",
	},
}

// Installer handles miner downloads and installations
type Installer struct {
	minersDir string
	tempDir   string
	debug     bool
}

// New creates a new Installer
func New(debug bool) *Installer {
	home, _ := os.UserHomeDir()
	return &Installer{
		minersDir: filepath.Join(home, "miners"),
		tempDir:   filepath.Join(os.TempDir(), "bloxos-miners"),
		debug:     debug,
	}
}

// SetMinersDir sets the directory where miners are installed
func (i *Installer) SetMinersDir(dir string) {
	i.minersDir = dir
}

// ListAvailable returns available miners
func (i *Installer) ListAvailable() map[string]MinerInfo {
	return AvailableMiners
}

// ListInstalled returns installed miners
func (i *Installer) ListInstalled() ([]string, error) {
	var installed []string

	entries, err := os.ReadDir(i.minersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return installed, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			// Check if binary exists
			info, ok := AvailableMiners[entry.Name()]
			if ok {
				binPath := filepath.Join(i.minersDir, entry.Name(), info.BinaryName)
				if _, err := os.Stat(binPath); err == nil {
					installed = append(installed, entry.Name())
				}
			}
		}
	}

	return installed, nil
}

// Install downloads and installs a miner
func (i *Installer) Install(minerName string) error {
	info, ok := AvailableMiners[minerName]
	if !ok {
		return fmt.Errorf("unknown miner: %s", minerName)
	}

	// Check OS compatibility
	if runtime.GOOS != "linux" && info.SupportedOS == "linux" {
		return fmt.Errorf("%s only supports Linux", info.Name)
	}

	fmt.Printf("Installing %s...\n", info.Name)

	// Get latest release from GitHub
	version, downloadURL, err := i.getLatestRelease(info)
	if err != nil {
		return fmt.Errorf("failed to get latest release: %w", err)
	}

	if i.debug {
		fmt.Printf("Latest version: %s\n", version)
		fmt.Printf("Download URL: %s\n", downloadURL)
	}

	// Create temp directory
	if err := os.MkdirAll(i.tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(i.tempDir)

	// Download the file
	archivePath := filepath.Join(i.tempDir, filepath.Base(downloadURL))
	if err := i.downloadFile(downloadURL, archivePath); err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}

	// Create miner directory
	minerDir := filepath.Join(i.minersDir, minerName)
	if err := os.MkdirAll(minerDir, 0755); err != nil {
		return fmt.Errorf("failed to create miner dir: %w", err)
	}

	// Extract archive
	if err := i.extractArchive(archivePath, minerDir); err != nil {
		return fmt.Errorf("failed to extract: %w", err)
	}

	// Find and make binary executable
	binPath := i.findBinary(minerDir, info.BinaryName)
	if binPath == "" {
		return fmt.Errorf("binary not found after extraction")
	}

	if err := os.Chmod(binPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable: %w", err)
	}

	// If binary is in a subdirectory, move it up
	if filepath.Dir(binPath) != minerDir {
		newPath := filepath.Join(minerDir, info.BinaryName)
		if err := os.Rename(binPath, newPath); err != nil {
			// Try copy instead
			if err := copyFile(binPath, newPath); err != nil {
				return fmt.Errorf("failed to move binary: %w", err)
			}
		}
	}

	fmt.Printf("Installed %s %s to %s\n", info.Name, version, minerDir)
	return nil
}

// Uninstall removes a miner
func (i *Installer) Uninstall(minerName string) error {
	minerDir := filepath.Join(i.minersDir, minerName)
	
	if _, err := os.Stat(minerDir); os.IsNotExist(err) {
		return fmt.Errorf("miner %s is not installed", minerName)
	}

	if err := os.RemoveAll(minerDir); err != nil {
		return fmt.Errorf("failed to remove miner: %w", err)
	}

	fmt.Printf("Uninstalled %s\n", minerName)
	return nil
}

// getLatestRelease fetches the latest release info from GitHub
func (i *Installer) getLatestRelease(info MinerInfo) (version string, downloadURL string, err error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", info.Repo)

	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "BloxOS-Agent")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", err
	}

	version = strings.TrimPrefix(release.TagName, "v")

	// Find matching asset
	expectedPattern := fmt.Sprintf(info.AssetPattern, version)
	
	for _, asset := range release.Assets {
		// Try exact match first
		if asset.Name == expectedPattern {
			return version, asset.BrowserDownloadURL, nil
		}
		
		// Try case-insensitive match
		if strings.EqualFold(asset.Name, expectedPattern) {
			return version, asset.BrowserDownloadURL, nil
		}
		
		// Try partial match for Linux x64 assets
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, "linux") && 
		   (strings.Contains(name, "x64") || strings.Contains(name, "64")) &&
		   !strings.Contains(name, "arm") {
			return version, asset.BrowserDownloadURL, nil
		}
	}

	return "", "", fmt.Errorf("no matching release asset found for pattern: %s", expectedPattern)
}

// downloadFile downloads a file with progress
func (i *Installer) downloadFile(url, destPath string) error {
	fmt.Printf("Downloading from %s...\n", url)

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// extractArchive extracts tar.gz, tar.xz, tgz, or zip files
func (i *Installer) extractArchive(archivePath, destDir string) error {
	fmt.Printf("Extracting to %s...\n", destDir)

	ext := strings.ToLower(filepath.Ext(archivePath))
	name := strings.ToLower(archivePath)

	switch {
	case strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz"):
		return i.extractTarGz(archivePath, destDir)
	case strings.HasSuffix(name, ".tar.xz"):
		return i.extractTarXz(archivePath, destDir)
	case ext == ".zip":
		return i.extractZip(archivePath, destDir)
	default:
		return fmt.Errorf("unsupported archive format: %s", ext)
	}
}

func (i *Installer) extractTarGz(archivePath, destDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	return i.extractTar(gzr, destDir)
}

func (i *Installer) extractTarXz(archivePath, destDir string) error {
	// Use xz command for .tar.xz files
	cmd := exec.Command("tar", "-xJf", archivePath, "-C", destDir)
	return cmd.Run()
}

func (i *Installer) extractTar(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Prevent path traversal
		target := filepath.Join(destDir, header.Name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
			os.Chmod(target, os.FileMode(header.Mode))
		}
	}

	return nil
}

func (i *Installer) extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}

		io.Copy(out, rc)
		out.Close()
		rc.Close()
		os.Chmod(target, f.Mode())
	}

	return nil
}

// findBinary searches for the binary in the extracted directory
func (i *Installer) findBinary(dir, binaryName string) string {
	var found string
	
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() == binaryName {
			found = path
			return filepath.SkipAll
		}
		return nil
	})

	return found
}

// copyFile copies a file
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	return out.Sync()
}

// GetMinerPath returns the path to an installed miner's binary
func (i *Installer) GetMinerPath(minerName string) string {
	info, ok := AvailableMiners[minerName]
	if !ok {
		return ""
	}
	
	path := filepath.Join(i.minersDir, minerName, info.BinaryName)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	
	return ""
}
