package lb

import (
	"log"
	"net/http"

	"github.com/pelageech/BDUTS/cache"
)

// SaveToCache takes all the necessary information about a response and saves it
// in cache
func (lb *LoadBalancer) SaveToCache(req *http.Request, resp *http.Response, byteArray []byte) {
	if !(resp.StatusCode >= 200 && resp.StatusCode < 400) {
		return
	}
	log.Println("Saving response in cache")

	go func() {
		cacheItem := &cache.Page{
			Body:   byteArray,
			Header: resp.Header,
		}

		key := req.Context().Value(cache.Hash).([]byte)
		if err := lb.cacheProps.InsertPageInCache(key, req, resp, cacheItem); err != nil {
			log.Println("Unsuccessful operation: ", err)
			return
		}
		log.Println("Successfully saved")
	}()
}