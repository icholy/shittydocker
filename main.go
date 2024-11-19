package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

func main() {
	// parse args
	var image string
	flag.StringVar(&image, "image", "alpine", "image to run")
	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatal("command is required")
	}
	// create chroot dir
	jail, err := os.MkdirTemp("", "jail-")
	if err != nil {
		log.Fatalf("failed to create jail: %s", err)
	}
	// download/extract image to dir
	if err := FetchImageTo("library", image, jail); err != nil {
		log.Fatalf("failed to fetch image: %s", err)
	}
	// run isolated process
	cmd := exec.Cmd{
		Path: flag.Arg(0),
		Args: flag.Args()[1:],
		Dir:  "/",
		Env:  []string{"/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		SysProcAttr: &syscall.SysProcAttr{
			Chroot:     jail,
			Cloneflags: syscall.CLONE_NEWPID,
		},
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
	}
	if err := cmd.Run(); err != nil {
		log.Printf("ERROR: %v", err)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}

func FetchImageTo(library, image, dir string) error {
	token, err := FetchRegistryToken(library, image)
	if err != nil {
		return err
	}
	manifests, err := ListManifests(library, image, token)
	if err != nil {
		return err
	}
	manifest, ok := FindManifest(manifests, Platform{
		Architecture: runtime.GOARCH,
		OS:           runtime.GOOS,
	})
	if !ok {
		return fmt.Errorf("manifest not found")
	}
	layers, err := ListLayers(library, image, manifest, token)
	if err != nil {
		return err
	}
	for _, layer := range layers {
		log.Printf("downloading layer %s/%s: %s", library, image, layer.Digest)
		data, err := FetchLayer(library, image, layer, token)
		if err != nil {
			return err
		}
		// NOTE: shelling out here because I couldn't figure out how
		//       to extract symlinks using archive/tar
		cmd := exec.Command("tar", "-xzf", "-", "-C", dir)
		cmd.Stdin = bytes.NewReader(data)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to untar: %v", err)
		}
	}
	return nil
}

func FetchRegistryToken(library, image string) (string, error) {
	var body struct {
		Token string `json:"token"`
	}
	url := fmt.Sprintf("https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s/%s:pull", library, image)
	res, err := http.DefaultClient.Get(url)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Token, nil
}

type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type Layer struct {
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
	Digest    string `json:"digest"`
}

type Manifest struct {
	Annotations map[string]string `json:"annotations"`
	Digest      string            `json:"digest"`
	MediaType   string            `json:"mediaType"`
	Platform    Platform          `json:"platform"`
	Size        int               `json:"size"`
}

func ListManifests(library, image, token string) ([]Manifest, error) {
	url := fmt.Sprintf("https://registry.hub.docker.com/v2/%s/%s/manifests/latest", library, image)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}
	var body struct {
		Manifests []Manifest `json:"manifests"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Manifests, nil
}

func FindManifest(manifests []Manifest, platform Platform) (Manifest, bool) {
	for _, m := range manifests {
		if m.Platform == platform {
			return m, true
		}
	}
	return Manifest{}, false
}

func ListLayers(library, image string, m Manifest, token string) ([]Layer, error) {
	url := fmt.Sprintf("https://registry.hub.docker.com/v2/%s/%s/manifests/%s", library, image, m.Digest)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}
	var body struct {
		Layers []Layer `json:"layers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Layers, nil
}

func FetchLayer(library, image string, l Layer, token string) ([]byte, error) {
	url := fmt.Sprintf("https://registry.hub.docker.com/v2/%s/%s/blobs/%s", library, image, l.Digest)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}
	return io.ReadAll(res.Body)
}
