package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/maxsei/Gophercises/sitemap"
)

func main() {
	file := flag.String("file", "stdout", "Save to xml/{name}.xlm directory otherwise xml is printed to stdout.")
	maxDepth := flag.Int("depth", -1, "How many layers deep the map will go.")
	concurrency := flag.Int("cc", 1, "Run program in a serial manner press 0.")
	siteUrl := flag.String("url", "https://gophercises.com", "Home page for the website to be mapped.")
	flag.Parse()
	//pick which function to use for crawling
	fns := []interface{}{
		sitemap.SiteNodes,
		sitemap.SiteNodesConcurrent,
	}

	start := time.Now()

	node, err := fns[*concurrency].(func(string, ...int) (*sitemap.SiteNode, error))(*siteUrl, *maxDepth)
	if err != nil {
		log.Fatal(err)
	}
	//open or create xml file unless a file of the same name exists
	xmlFile, err := fileOption(*file)
	if err != nil {
		log.Fatal(err)
	}
	defer xmlFile.Close()

	//print out total number of links and output xml to file
	var numLinks int
	if *file == "stdout" {
		numLinks = node.NodeToXML(os.Stdout)
	} else {
		numLinks = node.NodeToXML(xmlFile)
	}
	fmt.Printf("Finished in %v\n\n", time.Since(start))
	fmt.Printf("Total of %d links\n", numLinks)
}
func fileOption(file string) (*os.File, error) {
	path := "../xml/" + file + ".xml"
	if file == "stdout" {
		return os.Stdout, nil
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return nil, fmt.Errorf("File with path %s already exists.\n", path)
	}
	xmlFile, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return nil, err
	}
	return xmlFile, nil
}
