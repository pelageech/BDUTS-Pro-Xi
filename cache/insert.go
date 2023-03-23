package cache

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/boltdb/bolt"
)

var (
	infinityTime = time.Unix(0, 0).AddDate(7999, 12, 31)
	nullTime     = time.Time{}
)

// InsertPageInCache stores a new page in cache or rewrites the current page.
// First, it adds PageMetadata in DB and then the function starts a process of
// transactional writing the page on a disk.
// Page transforms to json-file.
func (props *CachingProperties) InsertPageInCache(req *http.Request, resp *http.Response, item *Page) error {
	var byteInfo, bytePage []byte
	var err error

	requestDirectives := loadRequestDirectives(req.Header)
	responseDirectives := loadResponseDirectives(resp.Header)

	if requestDirectives.NoStore || responseDirectives.NoStore {
		return errors.New("can't be stored in cache")
	}

	info := createCacheInfo(resp, int64(len(item.Body)))
	if byteInfo, err = json.Marshal(*info); err != nil {
		return err
	}

	if bytePage, err = json.Marshal(*item); err != nil {
		return err
	}

	keyString := props.constructKeyFromRequest(req)
	requestHash := hash([]byte(keyString))

	if err = insertPageMetadataToDB(props.DB(), requestHash, byteInfo); err != nil {
		return err
	}

	if err = writePageToDisk(requestHash, bytePage); err != nil {
		return err
	}

	log.Println("Successfully saved, page's size = ", info.Size)

	return nil
}

func insertPageMetadataToDB(db *bolt.DB, requestHash []byte, value []byte) error {
	return db.Update(func(tx *bolt.Tx) error {
		treeBucket, err := tx.CreateBucketIfNotExists(requestHash)
		if err != nil {
			return err
		}

		err = treeBucket.Put([]byte(pageInfo), value)
		return err
	})
}

func writePageToDisk(requestHash []byte, value []byte) error {
	subhashLength := hashLength / subHashCount

	var subHashes [][]byte
	for i := 0; i < subHashCount; i++ {
		subHashes = append(subHashes, requestHash[i*subhashLength:(i+1)*subhashLength])
	}

	path := PagesPath
	for _, v := range subHashes {
		path += "/" + string(v)
	}

	if err := os.MkdirAll(path, 0770); err != nil {
		return err
	}

	file, err := os.Create(path + "/" + string(requestHash[:]))
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			log.Println("Write to disk error: ", err)
		}
	}(file)

	_, err = file.Write(value)
	return err
}

// Создаёт экземпляр структуры cache.PageMetadata, в которой хранится
// информация о странице, помещаемой в кэш.
func createCacheInfo(resp *http.Response, size int64) *PageMetadata {
	meta := &PageMetadata{
		Size:               size,
		ResponseDirectives: *loadResponseDirectives(resp.Header),
	}

	return meta
}
