// Stage the host's published USB worker before go:embed runs. This tool is
// needed only at build time. The running Hub never fetches executable code.
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

//go:embed releases.json
var releases []byte

type Asset struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
}
type Index struct {
	CandidateTag string           `json:"candidate_tag,omitempty"`
	Tag          string           `json:"tag"`
	Assets       map[string]Asset `json:"assets"`
}

func digest(path string) (string, int64) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, 160<<20+1))
	if err != nil {
		return "", 0
	}
	return hex.EncodeToString(hash.Sum(nil)), n
}

// Publish metadata as one file; simultaneous builds never see a truncated manifest.
func saveMetadata(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".manifest-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func stage(local, destination string) error {
	host := runtime.GOOS + "-" + runtime.GOARCH
	var index Index
	if err := json.Unmarshal(releases, &index); err != nil {
		return err
	}
	asset, found := index.Assets[host]
	fromRelease := local == ""
	if !fromRelease {
		raw, err := os.ReadFile(filepath.Join(local, host+".json"))
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &asset); err != nil {
			return err
		}
	} else if !found {
		return fmt.Errorf("no pinned USB worker has been published for %s; use the platform release executable or build with go build ./cmd/picobrew-hub for a network-only Hub", host)
	}
	name := "picobrew-usb-" + host
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if asset.Platform != host || asset.Name != name || asset.Bytes <= 0 || asset.Bytes > 160<<20 || !regexp.MustCompile("^[0-9a-f]{64}$").MatchString(asset.SHA256) {
		return errors.New("invalid USB platform artifact manifest")
	}
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	dest := filepath.Join(destination, "worker")
	meta := filepath.Join(destination, "manifest.json")
	metaData, err := json.Marshal(asset)
	if err != nil {
		return err
	}
	if hash, size := digest(dest); hash == asset.SHA256 && size == asset.Bytes {
		if raw, err := os.ReadFile(meta); err == nil && string(raw) == string(metaData) {
			return nil
		}
		return saveMetadata(meta, metaData)
	}
	var src io.ReadCloser
	if fromRelease {
		if !regexp.MustCompile(`^(usb-tools-)?v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(index.Tag) {
			return errors.New("invalid public USB release tag")
		}
		tags := []string{index.Tag}
		if index.CandidateTag != "" {
			if !regexp.MustCompile(`^usb-candidate-[1-9][0-9]*-[1-9][0-9]*$`).MatchString(index.CandidateTag) {
				return errors.New("invalid USB candidate tag")
			}
			tags = append(tags, index.CandidateTag)
		}
		client := http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			host := req.URL.Hostname()
			if len(via) > 4 || req.URL.Scheme != "https" || !(host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com")) {
				return errors.New("unexpected USB release redirect")
			}
			return nil
		}}
		for _, tag := range tags {
			url := "https://github.com/jeffreydwalter/picobrew/releases/download/" + tag + "/" + name
			resp, err := client.Get(url)
			if err != nil {
				return err
			}
			if resp.StatusCode == http.StatusOK {
				src = resp.Body
				break
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				return fmt.Errorf("download published USB worker: %s", resp.Status)
			}
		}
		if src == nil {
			return errors.New("the pinned USB worker is unavailable on GitHub")
		}
	} else {
		f, e := os.Open(filepath.Join(local, name))
		if e != nil {
			return e
		}
		src = f
	}
	defer src.Close()
	temp, err := os.CreateTemp(destination, ".worker-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(src, asset.Bytes+1))
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n != asset.Bytes || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return errors.New("USB worker size or SHA-256 differs from the release manifest")
	}
	if err = os.Rename(temp.Name(), dest); err != nil {
		return err
	}
	return saveMetadata(meta, metaData)
}

func main() {
	local := flag.String("local", "", "Explicit directory containing the native build_worker.py output")
	destination := flag.String("dest", "internal/usbservice/helper/payload", "USB embed staging directory")
	flag.Parse()
	if err := stage(*local, *destination); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
