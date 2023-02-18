package cache

// запрос HEAD не на каждое обращение не каждые несколько секунд

// transfer encoding gz

// http 1.1 ranch

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/boltdb/bolt"
	"github.com/pelageech/BDUTS/config"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	reading = iota
	writing
	silent
	hashLength   = sha256.Size
	subHashCount = 8 // Количество подотрезков хэша
)

type Info struct {
	dateOfDeath time.Time // nil if undying
	remoteAddr  string
	status      int
}

// GetCacheIfExists Обращается к диску для нахождения ответа на запрос.
// Если таковой имеется - он возвращается, в противном случае выдаётся ошибка
func GetCacheIfExists(db *bolt.DB, req *http.Request) ([]byte, error) {
	keyString := constructKeyFromRequest(req)

	responseByteArray, err := getRecord(db, []byte(keyString))
	if err != nil {
		return nil, err
	}

	return responseByteArray, nil
}

// PutRecordInCache Помещает новую запись в кэш.
// Считает хэш аттрибутов запроса, по нему проходит вниз по дереву
// и записывает как лист новую запись.
func PutRecordInCache(db *bolt.DB, req *http.Request, resp *http.Response, responseByteArray []byte) error {
	if !isStorable(req) {
		return errors.New("can't be stored in cache:(")
	}
	info := createCacheInfo(req)
	value, err := json.Marshal(info)
	if err != nil {
		return err
	}

	keyString := constructKeyFromRequest(req)
	err = addNewRecord(db, []byte(keyString), value)
	return err
}

// OpenDatabase Открывает базу данных для дальнейшего использования
func OpenDatabase(path string) (*bolt.DB, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}
	return db, nil
}

// CloseDatabase Закрывает базу данных
func CloseDatabase(db *bolt.DB) {
	err := db.Close()
	if err != nil {
		log.Fatalln(err)
	}
}

// Добавляет новую запись в кэш.
func addNewRecord(db *bolt.DB, key []byte, value []byte) error {
	requestHash := hash(key)
	subhashLength := hashLength / subHashCount

	var subHashes [][]byte
	for i := 0; i < subHashCount; i++ {
		subHashes = append(subHashes, requestHash[i*subhashLength:(i+1)*subhashLength])
	}

	err := db.Update(func(tx *bolt.Tx) error {
		treeBucket, err := tx.CreateBucketIfNotExists(subHashes[0])
		if err != nil {
			return err
		}

		for i := 1; i < subHashCount; i++ {
			treeBucket, err = treeBucket.CreateBucketIfNotExists(subHashes[i])
			if err != nil {
				return err
			}
		}

		err = treeBucket.Put(key, value)
		return err
	})

	return err
}

// Найти элемент по ключу
// Ключ переводится в хэш, тот разбивается на подотрезки - названия бакетов
// Проходом по подотрезкам находим по ключу ответ на запрос
func getRecord(db *bolt.DB, key []byte) ([]byte, error) {
	var result []byte = nil

	requestHash := hash(key)
	subhashLength := hashLength / subHashCount

	var subHashes [][]byte
	for i := 0; i < subHashCount; i++ {
		subHashes = append(subHashes, requestHash[i*subhashLength:(i+1)*subhashLength])
	}

	err := db.View(func(tx *bolt.Tx) error {
		treeBucket := tx.Bucket(subHashes[0])
		if treeBucket == nil {
			return errors.New("miss cache")
		}
		for i := 1; i < subHashCount; i++ {
			treeBucket = treeBucket.Bucket(subHashes[i])
			if treeBucket == nil {
				return errors.New("miss cache")
			}
		}

		result = treeBucket.Get(key)
		if result == nil {
			return errors.New("no record in cache")
		}

		return nil
	})

	return result, err
}

// Удаляет запись из кэша
/*
func deleteRecord(db *bolt.DB, key []byte) error {
	requestHash := hash(key)
	subhashLength := hashLength / subHashCount

	var subHashes [][]byte
	for i := 0; i < subHashCount; i++ {
		subHashes = append(subHashes, requestHash[i*subhashLength:(i+1)*subhashLength])
	}

	err := db.Update(func(tx *bolt.Tx) error {
		treeBucket := tx.Bucket(subHashes[0])
		if treeBucket == nil {
			return errors.New("miss cache")
		}
		for i := 1; i < subHashCount; i++ {
			treeBucket := treeBucket.Bucket(subHashes[i])
			if treeBucket == nil {
				return errors.New("miss cache")
			}
		}

		err := treeBucket.Delete(key)

		return err
	})

	return err
}

// Сохраняет копию базы данных в файл
/*
func makeSnapshot(db *bolt.DB, filename string) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}

	err = db.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(f)
		return err
	})

	return err
}
*/

// Возвращает хэш от набора байт
func hash(value []byte) [hashLength]byte {
	hash := sha256.Sum256(value)
	return hash
}

func constructKeyFromRequest(req *http.Request) string {
	result := ""
	for _, addStringKey := range config.RequestKey {
		result += addStringKey(req)
	}
	return result
}

func isStorable(req *http.Request) bool {
	header := req.Header
	cacheControlString := header.Get("cache-control")
	if len(cacheControlString) == 0 {
		return false
	}

	// check if we shouldn't store the page
	cacheControl := strings.Split(cacheControlString, ";")
	for _, v := range cacheControl {
		if v == "no-store" {
			return false
		}
	}
	return true
}

func createCacheInfo(req *http.Request) *Info {
	var info Info

	info.remoteAddr = req.RemoteAddr

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
	}

	return &info
}
