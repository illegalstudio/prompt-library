package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"strings"
)

var safe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// Identity uses origin, or the only configured remote. Local paths are not portable identities.
func Identity(dir string) (string, error) {
	run := func(args ...string) (string, error) {
		b, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		return strings.TrimSpace(string(b)), err
	}
	if _, err := run("rev-parse", "--show-toplevel"); err != nil {
		return "", nil
	}
	remote, err := run("remote", "get-url", "origin")
	if err != nil {
		names, e := run("remote")
		if e != nil {
			return "", e
		}
		fields := strings.Fields(names)
		if len(fields) != 1 {
			return "", nil
		}
		remote, err = run("remote", "get-url", fields[0])
		if err != nil {
			return "", err
		}
	}
	return Canonical(remote)
}

func Canonical(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	var host, repo string
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return "", fmt.Errorf("invalid project remote: %w", err)
		}
		if u.Scheme == "file" || u.Hostname() == "" {
			return "", nil
		}
		if u.Scheme != "ssh" && u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "git" {
			return "", nil
		}
		host, repo = strings.ToLower(u.Hostname()), u.Path
	} else if i := strings.Index(remote, ":"); i > 0 && !strings.ContainsAny(remote[:i], `/\`) {
		host, repo = remote[:i], remote[i+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if len(host) == 1 {
			return "", nil
		} // Windows drive.
		host = strings.ToLower(host)
	} else {
		return "", nil
	}
	repo = strings.TrimSuffix(strings.Trim(strings.TrimSpace(repo), "/"), ".git")
	if repo == "" || strings.Contains(repo, "..") {
		return "", fmt.Errorf("invalid project repository path")
	}
	// GitHub and Bitbucket repository paths are case-insensitive. Other hosts may not be.
	if host == "github.com" || host == "bitbucket.org" {
		repo = strings.ToLower(repo)
	}
	return host + "/" + repo, nil
}

func Folder(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	label := safe.ReplaceAllString(path.Base(identity), "-")
	if len(label) > 48 {
		label = label[:48]
	}
	return label + "-" + hex.EncodeToString(sum[:8])
}
