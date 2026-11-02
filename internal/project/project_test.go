package project

import "testing"

func TestCanonical(t *testing.T) {
	for _, tt := range []struct{ url, want string }{
		{"git@github.com:Acme/Repo.git", "github.com/acme/repo"},
		{"https://user:password@GITHUB.COM/Acme/Repo.git/", "github.com/acme/repo"},
		{"ssh://git@github.com:22/acme/repo.git", "github.com/acme/repo"},
		{"https://gitlab.example/Team/Repo.git", "gitlab.example/Team/Repo"},
		{"git@gitlab.example:Team/Repo.git", "gitlab.example/Team/Repo"},
		{"/home/user/project", ""}, {"../repo", ""}, {"file:///tmp/repo", ""}, {`C:\repos\project`, ""},
	} {
		t.Run(tt.url, func(t *testing.T) {
			got, err := Canonical(tt.url)
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
func TestFolderAvoidsCollisions(t *testing.T) {
	a := Folder("github.com/acme/api")
	b := Folder("github.com/other/api")
	if a == b {
		t.Fatal("different remotes share a folder")
	}
	if Folder("github.com/acme/api") != a {
		t.Fatal("folder is unstable")
	}
}
