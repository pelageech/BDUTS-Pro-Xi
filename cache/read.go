package cache

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/boltdb/bolt"
)

// GetCacheIfExists Обращается к диску для нахождения ответа на запрос.
// Если таковой имеется - он возвращается, в противном случае выдаётся ошибка
func GetCacheIfExists(db *bolt.DB, req *http.Request) (*Item, error) {
	keyString := constructKeyFromRequest(req)
	requestHash := hash([]byte(keyString))

	//err := setStatusReading(db, requestHash)
	//if err != nil {
	//	return nil, err
	//}
	//defer func(db *bolt.DB, requestHash []byte) {
	//	err := setStatusSilent(db, requestHash)
	//	if err != nil {
	//		log.Fatalln(err)
	//	}
	//}(db, requestHash)

	info, err := getPageInfo(db, requestHash)
	if err != nil {
		return nil, err
	}

	if time.Now().After(info.DateOfDeath) {
		// delete
		return nil, errors.New("not fresh")
	}

	if info.IsPrivate && req.RemoteAddr != info.RemoteAddr {
		return nil, errors.New("private page: addresses are not equal")
	}

	bytes, err := readPageFromDisk(requestHash)
	if err != nil {
		return nil, err
	}

	var item Item
	err = json.Unmarshal(bytes, &item)
	if err != nil {
		return nil, err
	}

	return &item, nil
}

// Найти элемент по ключу
// Ключ переводится в хэш, тот разбивается на подотрезки - названия бакетов
// Проходом по подотрезкам находим по ключу ответ на запрос
func getPageInfo(db *bolt.DB, requestHash []byte) (*Info, error) {
	var result []byte = nil

	err := db.View(func(tx *bolt.Tx) error {
		treeBucket, err := getBucket(tx, requestHash)
		if err != nil {
			return err
		}

		result = treeBucket.Get(requestHash[:])
		if result == nil {
			return errors.New("no record in cache")
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	var info Info
	err = json.Unmarshal(result, &info)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

func readPageFromDisk(requestHash []byte) ([]byte, error) {
	subhashLength := hashLength / subHashCount

	var subHashes [][]byte
	for i := 0; i < subHashCount; i++ {
		subHashes = append(subHashes, requestHash[i*subhashLength:(i+1)*subhashLength])
	}

	path := root
	for _, v := range subHashes {
		path += string(v) + "/"
	}
	path += string(requestHash[:])

	bytes, err := os.ReadFile(path)
	return bytes, err
}
