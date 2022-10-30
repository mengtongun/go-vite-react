package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"

	"github.com/gin-gonic/gin"
	gv "github.com/torenware/vite-go"
)

// this is not for vite, but to help our
// makefile stop the process:
var pidFile string
var pidDeleteChan chan os.Signal

func waitForSignal() {
	if pidFile == "" {
		return
	}
	pidDeleteChan = make(chan os.Signal, 1)
	signal.Notify(pidDeleteChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-pidDeleteChan
		fmt.Println("Deleted pid file")
		_ = os.Remove(pidFile)
		os.Exit(0)

	}()
}

var frontendData *gv.VueGlue

//go:embed frontend
var dist embed.FS

func serveOneFile(w http.ResponseWriter, r *http.Request, uri, contentType string) {
	strippedURI := uri[1:]
	buf, err := fs.ReadFile(frontendData.DistFS, strippedURI)
	if err != nil {
		// Try public dir
		buf, err = fs.ReadFile(frontendData.DistFS, "public/"+strippedURI)
	}

	// If we ended up nil, render the file out.
	if err == nil {
		// not an error; letting the error case fall through
		w.Header().Add("Content-Type", contentType)
		w.Write(buf)
		return
	}

	// Otherwise, we cannot handle it, so 404 it is.
	w.WriteHeader(http.StatusNotFound)
}

func pageWithAVue(w http.ResponseWriter, r *http.Request) {

	re := regexp.MustCompile(`^/([^.]+)\.(svg|ico|jpg)$`)
	matches := re.FindStringSubmatch(r.RequestURI)
	if matches != nil {
		if frontendData.Environment == "development" {
			log.Printf("vite logo requested")
			url := frontendData.BaseURL + r.RequestURI
			http.Redirect(w, r, url, http.StatusPermanentRedirect)
			return
		} else {
			// production; we need to render this ourselves.
			var contentType string
			switch matches[2] {
			case "svg":
				contentType = "image/svg+xml"
			case "ico":
				contentType = "image/x-icon"
			case "jpg":
				contentType = "image/jpeg"
			}

			serveOneFile(w, r, r.RequestURI, contentType)
			return
		}

	}

	// our go page, which will host our javascript.
	t, err := template.ParseFiles("./test-template.tmpl")
	if err != nil {
		log.Fatal(err)
	}

	t.Execute(w, frontendData)
}

func main() {
	var config gv.ViteConfig

	flag.StringVar(&config.Environment, "env", "development", "development|production")
	flag.StringVar(&config.JSProjectPath, "assets", "", "location of javascript files.")
	flag.StringVar(&config.DevServerDomain, "domain", "localhost", "Domain of the dev server.")
	flag.BoolVar(&config.HTTPS, "https", false, "Expect dev server to use HTTPS")
	flag.StringVar(&config.AssetsPath, "dist", "", "dist directory relative to the JS project directory.")
	flag.StringVar(&config.EntryPoint, "entryp", "", "relative path of the entry point of the js app.")
	flag.StringVar(&config.Platform, "platform", "", "vue|react|svelte")
	flag.StringVar(&pidFile, "pid", "", "location of optional pid file.")
	flag.Parse()

	if pidFile != "" {
		pid := strconv.Itoa(os.Getpid())
		_ = os.WriteFile(pidFile, []byte(pid), 0644)
		waitForSignal()
	}
	defer func() {
		if pidFile != "" {
			err := os.Remove(pidFile)
			if err != nil {
				log.Printf("could not delete pid file: %v", err)
			}
		}
	}()

	if config.EntryPoint == "production" {
		config.FS = os.DirFS("frontend")
	} else {
		// Use the embed.
		config.FS = dist
	}

	if config.Environment == "production" {
		config.URLPrefix = "/assets/"
	} else if config.Environment == "development" {
		config.FS = os.DirFS("frontend")
		log.Printf("pulling defaults using package.json")
	} else {
		log.Fatalln("unsupported environment setting")
	}

	glue, err := gv.NewVueGlue(&config)
	if err != nil {
		log.Fatalln(err)
		return
	}
	frontendData = glue
	g := gin.Default()
	g.Use(gin.Logger())
	g.Use(gin.Recovery())

	fsHandler, err := glue.FileServer()
	if err != nil {
		log.Println("could not set up static file server", err)
		return
	}
	g.GET(config.URLPrefix+"/*action", gin.WrapH(fsHandler))
	g.GET("/", gin.WrapF(pageWithAVue))

	generatedConfig, _ := json.MarshalIndent(config, "", "  ")
	log.Println("Generated Configuration:\n", string(generatedConfig))

	err = g.Run(":4000")
	log.Fatal(err)

}
