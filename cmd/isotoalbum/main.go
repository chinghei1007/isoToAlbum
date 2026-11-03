package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"isotoalbum/internal/jobs"
	"isotoalbum/internal/sacd"
	"isotoalbum/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "address for the local web server")
	workingDir := flag.String("root", "", "base directory for output (defaults to the current directory)")
	flag.Parse()

	root := *workingDir
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			log.Fatalf("read working directory: %v", err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		log.Fatalf("resolve working directory: %v", err)
	}

	extractor := os.Getenv("SACD_EXTRACT_PATH")
	if extractor == "" {
		extractor = companionTool("sacd_extract")
	}
	ffmpeg := os.Getenv("FFMPEG_PATH")
	if ffmpeg == "" {
		ffmpeg = companionTool("ffmpeg")
	}
	tool := sacd.NewTool(extractor, ffmpeg)
	manager := jobs.NewManager(tool, root)
	handler, err := server.New(manager, tool)
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	fmt.Printf("ISO to Album is running at http://%s\n", *addr)
	fmt.Printf("Server output root: %s\n", root)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}

func companionTool(name string) string {
	executable, err := os.Executable()
	if err != nil {
		return name
	}
	if filepath.Ext(executable) == ".exe" {
		name += ".exe"
	}
	candidate := filepath.Join(filepath.Dir(executable), name)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return name
}
