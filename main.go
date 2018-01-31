// Copyright 2015 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Go-import-redirector is an HTTP server for a custom Go import domain.
// It responds to requests in a given import path root with a meta tag
// specifying the source repository for the ``go get'' command and an
// HTML redirect to the godoc.org documentation page for that package.
//
// Usage:
//
//	go-import-redirector [-addr address] [-tls] [-vcs sys] <import> <repo>
//
// Go-import-redirector listens on address (default ``:80'')
// and responds to requests for URLs in the given import path root
// with one meta tag specifying the given source repository for ``go get''
// and another meta tag causing a redirect to the corresponding
// godoc.org documentation page.
//
// For example, if invoked as:
//
//	go-import-redirector 9fans.net/go https://github.com/9fans/go
//
// then the response for 9fans.net/go/acme/editinacme will include these tags:
//
//	<meta name="go-import" content="9fans.net/go git https://github.com/9fans/go">
//	<meta http-equiv="refresh" content="0; url=https://godoc.org/9fans.net/go/acme/editinacme">
//
// If both <import> and <repo> end in /*, the corresponding path element
// is taken from the import path and substituted in repo on each request.
// For example, if invoked as:
//
//	go-import-redirector rsc.io/* https://github.com/rsc/*
//
// then the response for rsc.io/x86/x86asm will include these tags:
//
//	<meta name="go-import" content="rsc.io/x86 git https://github.com/rsc/x86">
//	<meta http-equiv="refresh" content="0; url=https://godoc.org/rsc.io/x86/x86asm">
//
// Note that the wildcard element (x86) has been included in the Git repo path.
//
// The -addr option specifies the HTTP address to serve (default ``:http'').
//
// The -tls option causes go-import-redirector to serve HTTPS on port 443,
// loading an X.509 certificate and key pair from files in the current directory
// named after the host in the import path with .crt and .key appended
// (for example, rsc.io.crt and rsc.io.key).
// Like for http.ListenAndServeTLS, the certificate file should contain the
// concatenation of the server's certificate and the signing certificate authority's certificate.
//
// The -vcs option specifies the version control system, git, hg, or svn (default ``git'').
//
// Deployment on Google Cloud Platform
//
// For the case of a redirector for an entire domain (such as rsc.io above),
// the Makefile in this directory contains recipes to deploy a trivial VM running
// just this program, using a static IP address that can be loaded into the
// DNS configuration for the target domain.
//
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"

	"rsc.io/letsencrypt"
)

var (
	addr             = flag.String("addr", ":http", "serve http on `address`")
	serveTLS         = flag.Bool("tls", false, "serve https on :443")
	vcs              = flag.String("vcs", "git", "set version control `system`")
	letsEncryptEmail = flag.String("letsencrypt", "", "use lets encrypt to issue TLS certificate, agreeing to TOS as `email` (implies -tls)")
	wildcard         bool
)

var (
	filePath                     string
	importCouplesWithoutWildCard map[string]string
	importCouplesWithWildCard    map[string]string
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: go-import-redirector <import> <repo>\n")
	fmt.Fprintf(os.Stderr, "usage (read from file): go-import-redirector <file path>\n")
	fmt.Fprintf(os.Stderr, "options:\n")
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, "examples:\n")
	fmt.Fprintf(os.Stderr, "\tgo-import-redirector rsc.io/* https://github.com/rsc/*\n")
	fmt.Fprintf(os.Stderr, "\tgo-import-redirector 9fans.net/go https://github.com/9fans/go\n")
	fmt.Fprintf(os.Stderr, "\tgo-import-redirector ~/User/my_imports_and_repos.txt\n")
	fmt.Fprintf(os.Stderr, "\n\texternal config file:\n")
	fmt.Fprintf(os.Stderr, "\t\t9fans.net/go https://github.com/9fans/go\n")
	os.Exit(2)
}

func main() {
	// log.SetFlags(0)
	log.SetPrefix("go-import-redirector: ")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() == 0 || flag.NArg() > 2 {
		flag.Usage()
	}

	hosts := []string{}
	importCouplesWithWildCard = map[string]string{}
	importCouplesWithoutWildCard = map[string]string{}

	// Read imports and repos from file
	if flag.NArg() == 1 {
		filePath = flag.Arg(0)
		if err := readFile(); err != nil {
			log.Fatal(err)
		}
	} else {
		importPath := strings.TrimSuffix(flag.Arg(0), "/") + "/"
		repoPath := strings.TrimSuffix(flag.Arg(1), "/") + "/"
		importCouplesWithoutWildCard[importPath] = repoPath
	}

	for importPath, repoPath := range importCouplesWithoutWildCard {
		if err := validateInput(importPath, repoPath); err != nil {
			log.Fatal(err)
		}
		if strings.HasSuffix(importPath, "/*") {
			delete(importCouplesWithoutWildCard, importPath)
			importPath = strings.TrimSuffix(importPath, "/*")
			repoPath = strings.TrimSuffix(repoPath, "/*")
			importCouplesWithWildCard[importPath+"/"] = repoPath + "/"
		}

		http.HandleFunc(importPath, redirect)
		http.HandleFunc(importPath+"/.ping", pong) // non-redirecting URL for debugging TLS certificates

		host := importPath
		if i := strings.Index(host, "/"); i >= 0 {
			host = host[:i]
		}
		hosts = append(hosts, host)
	}

	if !*serveTLS {
		log.Fatal(http.ListenAndServe(*addr, nil))
	}

	m := new(letsencrypt.Manager)
	m.CacheFile("letsencrypt.cache")
	m.SetHosts(hosts)

	if *letsEncryptEmail != "" && !m.Registered() {
		if err := m.Register(*letsEncryptEmail, nil); err != nil {
			log.Fatal(err)
		}
	}

	log.Fatal(m.Serve())
}

func validateInput(importPath string, repoPath string) error {
	if !strings.Contains(repoPath, "://") {
		log.Fatal("repo path must be full URL")
		return fmt.Errorf("repo path must be full URL")
	}
	if strings.HasSuffix(importPath, "/*") != strings.HasSuffix(importPath, "/*") {
		log.Fatal("either both import and repo must have /* or neither")
		return fmt.Errorf("either both import and repo must have /* or neither")
	}
	return nil
}

func readFile() error {
	log.Printf("Reading file: %s", filePath)
	reader, err := os.Open(filePath)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())

		switch len(fields) {
		case 0:
			continue
		case 2:
			importPath := strings.TrimSuffix(fields[0], "/") + "/"
			repoPath := strings.TrimSuffix(fields[1], "/") + "/"
			importCouplesWithoutWildCard[importPath] = repoPath
		default:
			return fmt.Errorf("file malformed: %s", scanner.Text())
		}
	}
	return nil
}

var tmpl = template.Must(template.New("main").Parse(`<!DOCTYPE html>
<html>
<head>
<meta http-equiv="Content-Type" content="text/html; charset=utf-8"/>
<meta name="go-import" content="{{.ImportRoot}} {{.VCS}} {{.VCSRoot}}">
<meta http-equiv="refresh" content="0; url=https://godoc.org/{{.ImportRoot}}{{.Suffix}}">
</head>
<body>
Redirecting to docs at <a href="https://godoc.org/{{.ImportRoot}}{{.Suffix}}">godoc.org/{{.ImportRoot}}{{.Suffix}}</a>...
</body>
</html>
`))

type data struct {
	ImportRoot string
	VCS        string
	VCSRoot    string
	Suffix     string
}

func redirect(w http.ResponseWriter, req *http.Request) {
	log.Print("In redirect")
	path := strings.TrimSuffix(req.Host+req.URL.Path, "/") + "/"
	var importRoot, repoRoot, suffix string
	if repoPath, ok := importCouplesWithoutWildCard[path]; ok {
		importRoot = path
		repoRoot = repoPath
		suffix = ""
	} else if importPath, ok := getImportPathForWildCard(path); ok {
		if path == importPath {
			http.Redirect(w, req, "https://godoc.org/"+repoPath, 302)
			return
		}
		elem := path[len(importPath):]
		if i := strings.Index(elem, "/"); i >= 0 {
			elem, suffix = elem[:i], elem[i:]
		}
		importRoot = importPath + elem
		repoRoot = repoPath + elem
	} else {
		http.NotFound(w, req)
		return
	}
	d := &data{
		ImportRoot: strings.TrimSuffix(importRoot, "/"),
		VCS:        *vcs,
		VCSRoot:    repoRoot,
		Suffix:     suffix,
	}
	log.Printf("data:\n ImportRoot: %s, VCS: %s, VCSRoot: %s, Suffix: %s", d.ImportRoot, d.VCS, d.VCSRoot, d.Suffix)
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, d)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write(buf.Bytes())
}
func getImportPathForWildCard(path string) (string, bool) {
	for importPath, _ := range importCouplesWithoutWildCard {
		if strings.HasPrefix(path, importPath) {
			return importPath, true
		}
	}
	return "", false
}

func pong(w http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(w, "pong")
}
