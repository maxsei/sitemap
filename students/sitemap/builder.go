package sitemap

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maxsei/Gophercises/linkparser"
)

// dataStore is an atomic key value store
// that is used for checking duplicate items
type dataStore struct {
	sync.Mutex // ← this mutex protects the cache below
	cache      map[string]bool
}

func newDataStore() *dataStore {
	return &dataStore{
		cache: make(map[string]bool),
	}
}
func (ds *dataStore) set(k string, v bool) {
	ds.Lock()
	defer ds.Unlock()
	ds.cache[k] = v
}
func (ds *dataStore) seen(k string) bool {
	ds.Lock()
	defer ds.Unlock()
	_, has := ds.cache[k]
	return has
}

// SiteNode holds infomation of how deep embedded it is
// in a site as well as its url and its other urls
// that link to other pages on the domain
type SiteNode struct {
	Depth    int
	Url      string
	Children []*SiteNode
}
type url struct {
	Url string `xml:"url"`
}
type urlnode struct {
	Urlnodes []urlnode `xml:urlnode`
	Url      string    `xml:"id,attr"`
	Urls     []url     `xml:url`
}

// SiteNodesConcurrent will crawl the domain in a concurrent manner
// such that the breadth first search layer blocks until all the
// goroutines have fineshed for a given layer the search.
// It returns a *Sitenode head that has preceding children SiteNodes.
func SiteNodesConcurrent(domainUrl string, maxDepthRaw ...int) (*SiteNode, error) {
	maxDepth, err := maxDepthHandler(maxDepthRaw...)
	if err != nil {
		return nil, err
	}
	rootNode := SiteNode{
		Depth:    0,
		Url:      domainUrl,
		Children: make([]*SiteNode, 0),
	}
	var visitedNodes = newDataStore()
	visitedNodes.set(rootNode.Url, true)

	deepestNodes := make(chan *SiteNode)
	doneNodeDepth := make(chan int)

	var nodesWorking int32 = 0
	var layerWG sync.WaitGroup

	var nodeChildren func(node *SiteNode) error
	nodeChildren = func(node *SiteNode) error {
		//fmt.Printf("Depth: %d\tLink: %s\tNodes working: %d\n", node.Depth, node.Url, nodesWorking)
		links, err := linksAtUrl(node.Url)
		if err != nil {
			return err
		}
		if len(links) == 0 {
			doneNodeDepth <- node.Depth //done using this node to find other nodes
			return nil
		}
		for _, link := range links {
			absurl := absoluteUrl(domainUrl, link.Href)
			if visitedNodes.seen(absurl) {
				continue
			}
			visitedNodes.set(absurl, true)
			nextNode := SiteNode{
				Depth:    node.Depth + 1,
				Url:      absurl,
				Children: make([]*SiteNode, 0),
			}
			node.Children = append(node.Children, &nextNode)
			deepestNodes <- &nextNode
		}
		doneNodeDepth <- node.Depth //done using this node to find other nodes
		return nil
	}
	nodeCountLayer := 0
	currentDepth := 0
	var wg sync.WaitGroup
	go func() { deepestNodes <- &rootNode }()
	for {
		select {
		case node := <-deepestNodes:
			go func() {
				if node.Depth == maxDepth {
					return
				}
				atomic.AddInt32(&nodesWorking, 1)
				if node.Depth > currentDepth {
					wg.Add(1)
					time.Sleep(time.Nanosecond)
					layerWG.Wait()
					wg.Done()
				}
				wg.Wait()
				layerWG.Add(1)
				err := nodeChildren(node) //can return an error
				if err != nil {
					log.Print(err)
				}
			}()
		case depth := <-doneNodeDepth:
			if depth > currentDepth {
				fmt.Printf("Layer %d traversed with %d nodes.\n", currentDepth, nodeCountLayer)
				nodeCountLayer = 0
				currentDepth = depth
			}
			nodeCountLayer++
			layerWG.Done()
			atomic.AddInt32(&nodesWorking, -1)
			//fmt.Printf("%d Nodes Working\n", nodesWorking)
			if nodesWorking == 0 {
				return &rootNode, nil
			}
		case <-time.After(15 * time.Second):
			return &rootNode, nil
		}
	}
	return &rootNode, nil
}

// SiteNodes crawls a domain in a serial fasionand returns
// a head *SiteNode with preceding children
// if a depth is not specified, crawling will have no depth limit
func SiteNodes(domainUrl string, maxDepthRaw ...int) (*SiteNode, error) {
	maxDepth, err := maxDepthHandler(maxDepthRaw...)
	if err != nil {
		return nil, err
	}
	rootNode := SiteNode{
		Depth:    0,
		Url:      domainUrl,
		Children: make([]*SiteNode, 0),
	}

	var visitedNodes = map[string]int8{rootNode.Url: 1}
	deepestNodes := append(make([]*SiteNode, 0), &rootNode)

	var nodeChildren func(node *SiteNode) error
	nodeChildren = func(node *SiteNode) error {
		links, err := linksAtUrl(node.Url)
		if err != nil {
			return err
		}
		for _, link := range links {
			absurl := absoluteUrl(domainUrl, link.Href)
			if _, seen := visitedNodes[absurl]; seen {
				continue
			}
			visitedNodes[absurl] = 1
			nextNode := SiteNode{
				Depth:    node.Depth + 1,
				Url:      absurl,
				Children: make([]*SiteNode, 0),
			}
			node.Children = append(node.Children, &nextNode)
			deepestNodes = append(deepestNodes, &nextNode)
		}
		return nil
	}
	for len(deepestNodes) > 0 {
		if deepestNodes[0].Depth == maxDepth && maxDepth != -1 {
			return &rootNode, nil
		}
		//fmt.Printf("Depth: %d\tLink: %s\n", deepestNodes[0].Depth, deepestNodes[0].Url)
		err := nodeChildren(deepestNodes[0])
		if err != nil {
			return nil, err
		}
		deepestNodes = deepestNodes[1:] // Dequeue
	}
	return &rootNode, nil
}

// performs a depth first search on a *SiteNode head
// to produce an xml document written to io.Writer.
// Also returns number of links parsed
func (rootNode *SiteNode) NodeToXML(w io.Writer) int {
	var numLinks int = 1
	var buildXML func(node *SiteNode) urlnode
	buildXML = func(node *SiteNode) urlnode {
		n := urlnode{
			Urlnodes: make([]urlnode, 0),
			Url:      node.Url,
			Urls:     make([]url, 0),
		}
		if len(node.Children) == 0 {
			return n
		}
		for _, v := range node.Children {
			n.Urlnodes = append(n.Urlnodes, buildXML(v))
			numLinks++
		}
		return n
	}
	rootNodeXML := buildXML(rootNode)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	err := enc.Encode(rootNodeXML)
	log.Print(err)
	return numLinks
}

// specifies a given depth or infinite depth if none is provided
func maxDepthHandler(maxDepth ...int) (int, error) {
	if len(maxDepth) == 0 {
		return -1, nil
	}
	if len(maxDepth) != 1 {
		return 0, fmt.Errorf("%d arguements for maxDepth were proved.  Either enter one or no arguements for max depth\n", len(maxDepth))
	}
	if maxDepth[0] < -1 {
		return 0, fmt.Errorf("%d was entered for maxDepth. Must be non negative.\n", len(maxDepth))
	}
	return maxDepth[0], nil
}

// get all links of the same domain from a html document
func linksAtUrl(url string) ([]linkparser.Link, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	links, err := linkparser.Parse(resp.Body)
	if err != nil {
		return nil, err
	}
	return links, nil
}

//pass in domain url and relative url
func absoluteUrl(root, branch string) string {
	if len(branch) < 2 {
		return root
	}
	if branch[0] == "/"[0] {
		return root + branch
	}
	if len(branch) < len(root) {
		return root
	}
	if branch[:len(root)] == root {
		return branch
	}
	return root
}
