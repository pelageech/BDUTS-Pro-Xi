package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/boltdb/bolt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// PutRecordInCache Помещает новую запись в кэш.
// Считает хэш аттрибутов запроса, по нему проходит вниз по дереву
// и записывает как лист новую запись.
func PutRecordInCache(db *bolt.DB, req *http.Request, resp []byte) error {
	if !isStorable(req) {
		return errors.New("can't be stored in cache:(")
	}

	info := createCacheInfo(req)

	valueInfo, err := json.Marshal(info)
	if err != nil {
		return err
	}

	keyString := constructKeyFromRequest(req)
	fmt.Println(keyString)
	requestHash := hash([]byte(keyString))
	err = putPageInfoIntoDB(db, requestHash, valueInfo)

	if err != nil {
		return err
	}
	err = writePageToDisk(requestHash, resp)
	return err
}

// Добавляет новую запись в кэш.
func putPageInfoIntoDB(db *bolt.DB, requestHash []byte, value []byte) error {
	err := db.Update(func(tx *bolt.Tx) error {
		treeBucket, err := tx.CreateBucketIfNotExists(requestHash)
		if err != nil {
			return err
		}

		err = treeBucket.Put(requestHash[:], value)
		return err
	})

	return err
}

func writePageToDisk(requestHash []byte, value []byte) error {
	subhashLength := hashLength / subHashCount

	var subHashes [][]byte
	for i := 0; i < subHashCount; i++ {
		subHashes = append(subHashes, requestHash[i*subhashLength:(i+1)*subhashLength])
	}

	path := root
	for _, v := range subHashes {
		path += string(v) + "/"
	}

	err := os.MkdirAll(path, 0770)
	if err != nil {
		return err
	}

	file, err := os.Create(path + string(requestHash[:]))
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

// Удаляет запись из кэша
//func deleteRecord(db *bolt.DB, key []byte) error {
//	requestHash := hash(key)
//	subhashLength := hashLength / subHashCount
//
//	var subHashes [][]byte
//	for i := 0; i < subHashCount; i++ {
//		subHashes = append(subHashes, requestHash[i*subhashLength:(i+1)*subhashLength])
//	}
//
//	err := db.Update(func(tx *bolt.Tx) error {
//		treeBucket := tx.Bucket(subHashes[0])
//		if treeBucket == nil {
//			return errors.New("miss cache")
//		}
//		for i := 1; i < subHashCount; i++ {
//			treeBucket := treeBucket.Bucket(subHashes[i])
//			if treeBucket == nil {
//				return errors.New("miss cache")
//			}
//		}
//
//		err := treeBucket.Delete(key)
//
//		return err
//	})
//
//	return err
//}

func createCacheInfo(req *http.Request) *Info {
	var info Info

	info.remoteAddr = req.RemoteAddr
	info.isPrivate = false

	header := req.Header
	cacheControlString := header.Get("cache-control")

	// check if we shouldn't store the page
	cacheControl := strings.Split(cacheControlString, ";")
	for _, v := range cacheControl {
		if strings.Contains(v, "max-age") {
			_, t, _ := strings.Cut(v, ":")
			age, _ := strconv.Atoi(t)
			if age > 0 {
				info.dateOfDeath = time.Now().Add(time.Duration(age) * time.Second)
			}
		}
		if strings.Contains(v, "private") {
			info.isPrivate = true
		}
	}

	return &info
}
