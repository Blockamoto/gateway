// Gateway's update publisher is a separate operator tool. It is deliberately
// absent from ordinary client packages and has its own distribution listener.
package main

import (
	"../../internal/updatepublisher"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Update publisher:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: update-publisher keygen|publish|publish-headers|renew|renew-all|restore|export|serve|serve-release|check (use SUBCOMMAND -h)")
	}
	switch args[0] {
	case "restore":
		flags := flag.NewFlagSet("restore", flag.ContinueOnError)
		bundle := flags.String("bundle", "", "authenticated delivery bundle ZIP")
		out := flags.String("out", "", "new private working directory")
		version := flags.String("version", "", "explicit approved release version")
		repository := flags.String("repository", updatepublisher.DefaultRepository, "explicit approved repository")
		public := flags.String("public", "", "independently provisioned public trust document")
		forRenewal := flags.Bool("for-renewal", false, "operator recovery only: authenticate expired metadata without making it servable")
		if e := flags.Parse(args[1:]); e != nil {
			return e
		}
		if *bundle == "" || *out == "" || *version == "" || *public == "" || flags.NArg() != 0 {
			return errors.New("restore requires bundle, out, version and public")
		}
		key, e := updatepublisher.ReadTrustedKey(*public)
		if e != nil {
			return e
		}
		info, e := os.Stat(*bundle)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
			return errors.New("invalid distribution bundle file or size")
		}
		raw, e := os.ReadFile(*bundle)
		if e != nil {
			return e
		}
		if *forRenewal {
			e = updatepublisher.RestoreDistributionForRenewal(raw, *out, *version, *repository, key)
		} else {
			e = updatepublisher.RestoreDistribution(raw, *out, *version, *repository, key)
		}
		if e != nil {
			return e
		}
		result, e := updatepublisher.InspectForRenewal(*out, key)
		if e != nil {
			return e
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	case "export":
		flags := flag.NewFlagSet("export", flag.ContinueOnError)
		dir := flags.String("dir", "", "verified local distribution")
		public := flags.String("public", "", "independently provisioned public trust document")
		out := flags.String("out", "", "new public distribution bundle ZIP")
		if e := flags.Parse(args[1:]); e != nil {
			return e
		}
		if *dir == "" || *public == "" || *out == "" || flags.NArg() != 0 {
			return errors.New("export requires dir, public and out")
		}
		key, e := updatepublisher.ReadTrustedKey(*public)
		if e != nil {
			return e
		}
		return updatepublisher.ExportDistribution(*dir, *out, key)
	case "serve-release":
		flags := flag.NewFlagSet("serve-release", flag.ContinueOnError)
		repository := flags.String("repository", updatepublisher.DefaultRepository, "explicit approved release repository")
		version := flags.String("version", "", "explicit published version; never follows latest")
		public := flags.String("public", "", "independently provisioned public trust document")
		listen := flags.String("listen", "127.0.0.1:8786", "distribution-only listener behind Render HTTPS proxy")
		if e := flags.Parse(args[1:]); e != nil {
			return e
		}
		if *version == "" || *public == "" || flags.NArg() != 0 {
			return errors.New("serve-release requires version and public")
		}
		if e := updatepublisher.ValidateListener(*listen, false, true); e != nil {
			return e
		}
		key, e := updatepublisher.ReadTrustedKey(*public)
		if e != nil {
			return e
		}
		root, e := os.MkdirTemp("", "gateway-render-feed-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(root)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		if e = updatepublisher.RestoreGitHubDistribution(ctx, *repository, *version, os.Getenv("GATEWAY_PUBLISHER_GITHUB_TOKEN"), filepath.Join(root, "feed"), key); e != nil {
			return e
		}
		handler, e := updatepublisher.Handler(filepath.Join(root, "feed"), key)
		if e != nil {
			return e
		}
		server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
		fmt.Println("Verified selected signed distribution; serving read-only update delivery.")
		return server.ListenAndServe()
	case "keygen":
		flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
		private := flags.String("private", "", "private signing-key file, kept only by the publisher")
		public := flags.String("public", "", "public trust document to independently provision to clients")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *private == "" || *public == "" || flags.NArg() != 0 {
			return errors.New("keygen requires -private and -public paths")
		}
		trusted, err := updatepublisher.GenerateKey(*private, *public)
		if err != nil {
			return err
		}
		fmt.Println("Created publisher key identity:", trusted.KeyID)
		fmt.Println("Protect the private key with owner-only filesystem permissions; provision the public trust file through an independent trusted channel.")
		return nil
	case "publish":
		flags := flag.NewFlagSet("publish", flag.ContinueOnError)
		version := flags.String("version", "", "explicit published GitHub version, newer than 0.6.3")
		repository := flags.String("repository", updatepublisher.DefaultRepository, "approved GitHub repository")
		approved := flags.String("approve-release", "", "repeat the selected version to authorize publisher signing promotion")
		sequence := flags.Uint64("sequence", 0, "positive monotonically increasing feed sequence")
		validity := flags.Duration("validity", 7*24*time.Hour, "manifest validity, at most 744h; renew before expiry")
		privatePath := flags.String("private", "", "publisher-only signing-key file outside distribution root")
		out := flags.String("out", "", "distribution directory containing immutable signed snapshots")
		fixture := flags.String("local-fixture", "", "LOCAL TEST ONLY: directory of prepared packages and artifact-manifest.json; never fetches GitHub")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *version == "" || *approved != *version || *sequence == 0 || *privatePath == "" || *out == "" || flags.NArg() != 0 {
			return errors.New("publish requires -version, matching -approve-release, -sequence, -private and -out")
		}
		privateAbsolute, err := filepath.Abs(*privatePath)
		if err != nil {
			return err
		}
		outAbsolute, err := filepath.Abs(*out)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(outAbsolute, privateAbsolute)
		if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return errors.New("publisher private key must be stored outside the distribution directory")
		}
		private, err := updatepublisher.ReadPrivateKey(*privatePath)
		if err != nil {
			return err
		}
		var origin updatepublisher.OriginRelease
		if *fixture != "" {
			origin, err = updatepublisher.ReadLocalFixture(*fixture, *version)
			fmt.Println("Preparing a LOCAL FIXTURE feed; these packages have no authenticated GitHub origin.")
		} else {
			downloadDir, tempErr := os.MkdirTemp("", "gateway-update-origin-")
			if tempErr != nil {
				return tempErr
			}
			defer os.RemoveAll(downloadDir)
			origin, err = updatepublisher.FetchGitHubRepository(context.Background(), *repository, *version, os.Getenv("GATEWAY_PUBLISHER_GITHUB_TOKEN"), downloadDir)
		}
		if err != nil {
			return err
		}
		manifest, err := updatepublisher.Publish(*out, origin, *sequence, *validity, private)
		if err != nil {
			return err
		}
		data, _ := json.MarshalIndent(manifest, "", "  ")
		fmt.Println(string(data))
		fmt.Println("Prepared signed local distribution snapshot. No GitHub release or public service was created.")
		return nil
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		directory := flags.String("dir", "", "signed distribution directory")
		publicPath := flags.String("public", "", "independently provisioned public trust document; no signing key needed")
		listen := flags.String("listen", "127.0.0.1:8786", "distribution-only host:port listener")
		certificate := flags.String("tls-cert", "", "HTTPS certificate")
		tlsKey := flags.String("tls-key", "", "HTTPS private key (not the update signing key)")
		reverseProxy := flags.Bool("behind-tls-proxy", false, "explicitly permit non-loopback HTTP only behind an operator-controlled HTTPS proxy")
		requireToken := flags.Bool("require-access-token", false, "require dedicated GATEWAY_PUBLISHER_ACCESS_TOKEN for distribution; never use the origin GitHub token")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *directory == "" || *publicPath == "" || flags.NArg() != 0 {
			return errors.New("serve requires -dir and -public")
		}
		if (*certificate == "") != (*tlsKey == "") {
			return errors.New("both -tls-cert and -tls-key are required for HTTPS")
		}
		if err := updatepublisher.ValidateListener(*listen, *certificate != "", *reverseProxy); err != nil {
			return err
		}
		trusted, err := updatepublisher.ReadTrustedKey(*publicPath)
		if err != nil {
			return err
		}
		handler, err := updatepublisher.Handler(*directory, trusted)
		if err != nil {
			return err
		}
		if *requireToken {
			token := os.Getenv("GATEWAY_PUBLISHER_ACCESS_TOKEN")
			if token == "" {
				return errors.New("GATEWAY_PUBLISHER_ACCESS_TOKEN is required")
			}
			if token == os.Getenv("GATEWAY_PUBLISHER_GITHUB_TOKEN") {
				return errors.New("distribution credential must differ from the GitHub origin credential")
			}
			handler, err = updatepublisher.ProtectDelivery(handler, token)
			if err != nil {
				return err
			}
		}
		server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
		fmt.Println("Serving signed update metadata and intended platform archives only at", *listen)
		if *certificate != "" {
			return server.ListenAndServeTLS(*certificate, *tlsKey)
		}
		return server.ListenAndServe()
	case "publish-headers":
		flags := flag.NewFlagSet("publish-headers", flag.ContinueOnError)
		repository := flags.String("repository", updatepublisher.DefaultRepository, "approved GitHub repository")
		version := flags.String("version", "", "explicit published version")
		approve := flags.String("approve-release", "", "repeat exact version to authorize header promotion")
		sequence := flags.Uint64("sequence", 0, "monotonic header-feed sequence, independent of application feed")
		validity := flags.Duration("validity", 7*24*time.Hour, "validity at most 744h")
		privatePath := flags.String("private", "", "signing key outside distribution")
		out := flags.String("out", "", "distribution directory")
		fixture := flags.String("local-fixture", "", "LOCAL TEST ONLY: header snapshot ZIP")
		if e := flags.Parse(args[1:]); e != nil {
			return e
		}
		if *version == "" || *approve != *version || *sequence == 0 || *privatePath == "" || *out == "" || flags.NArg() != 0 {
			return errors.New("publish-headers requires version, matching approval, sequence, private and out")
		}
		keyPath, e := filepath.Abs(*privatePath)
		if e != nil {
			return e
		}
		dir, e := filepath.Abs(*out)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(dir, keyPath)
		if e != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return errors.New("signing key must be outside distribution")
		}
		private, e := updatepublisher.ReadPrivateKey(keyPath)
		if e != nil {
			return e
		}
		var snapshot updatepublisher.HeaderSnapshot
		if *fixture != "" {
			raw, err := os.ReadFile(*fixture)
			if err != nil {
				return err
			}
			snapshot, e = updatepublisher.ReadHeaderSnapshotArchive(raw, "https://github.com/"+*repository+"/releases/tag/v"+*version)
		} else {
			snapshot, e = updatepublisher.FetchHeaderSnapshotGitHub(context.Background(), *repository, *version, os.Getenv("GATEWAY_PUBLISHER_GITHUB_TOKEN"))
		}
		if e != nil {
			return e
		}
		manifest, e := updatepublisher.PublishHeaders(dir, snapshot, *sequence, *validity, private)
		if e != nil {
			return e
		}
		raw, _ := json.MarshalIndent(manifest, "", "  ")
		fmt.Println(string(raw))
		return nil
	case "renew", "renew-all":
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		directory := flags.String("out", "", "previously signed distribution directory")
		privatePath := flags.String("private", "", "publisher signing key outside distribution")
		approved := flags.String("approve-release", "", "exact already promoted version; never selects a new release")
		validity := flags.Duration("validity", 7*24*time.Hour, "renewed validity, at most 744h")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *directory == "" || *privatePath == "" || *approved == "" || flags.NArg() != 0 {
			return errors.New("renew requires -out, -private and -approve-release")
		}
		privateAbsolute, err := filepath.Abs(*privatePath)
		if err != nil {
			return err
		}
		outAbsolute, err := filepath.Abs(*directory)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(outAbsolute, privateAbsolute)
		if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return errors.New("publisher private key must be outside distribution")
		}
		private, err := updatepublisher.ReadPrivateKey(*privatePath)
		if err != nil {
			return err
		}
		var manifest any
		if args[0] == "renew-all" {
			manifest, err = updatepublisher.RenewAll(*directory, *approved, *validity, private)
		} else {
			manifest, err = updatepublisher.Renew(*directory, *approved, *validity, private)
		}
		if err != nil {
			return err
		}
		data, _ := json.MarshalIndent(manifest, "", "  ")
		fmt.Println(string(data))
		return nil
	case "check":
		flags := flag.NewFlagSet("check", flag.ContinueOnError)
		directory := flags.String("dir", "", "signed distribution directory")
		public := flags.String("public", "", "independently provisioned public trust document")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *directory == "" || *public == "" || flags.NArg() != 0 {
			return errors.New("check requires -dir and -public")
		}
		trusted, err := updatepublisher.ReadTrustedKey(*public)
		if err != nil {
			return err
		}
		manifest, err := updatepublisher.CheckDistribution(*directory, trusted)
		if err != nil {
			return err
		}
		data, _ := json.MarshalIndent(manifest, "", "  ")
		fmt.Println(string(data))
		return nil
	default:
		return errors.New("unknown publisher command; use keygen, publish, publish-headers, renew, renew-all, restore, export, serve, serve-release or check")
	}
}
