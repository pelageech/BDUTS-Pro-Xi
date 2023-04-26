package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	_ "github.com/lib/pq"
	"github.com/pelageech/BDUTS/db"
	"github.com/pelageech/BDUTS/email"
	"golang.org/x/crypto/bcrypt"
)

const (
	passwordLength = 25
	saltLength     = 20

	subject     = "Your credentials for BDUTS load balancer"
	msgTemplate = "Your username: %s\n" +
		"Your password: %s\n\n" +
		"Please log in and change your password.\n" +
		"By changing your temporary password, you're helping to ensure that your account is secure " +
		"and that only you have access to it. It's also an opportunity to choose a password " +
		"that's easy for you to remember, but difficult for others to guess."
)

type Service struct {
	db        db.Service
	sender    *email.Sender
	validator *validator.Validate
	signKey   []byte
}

func New(db db.Service, sender *email.Sender, validator *validator.Validate, signKey []byte) *Service {
	return &Service{
		db:        db,
		sender:    sender,
		validator: validator,
		signKey:   signKey,
	}
}

type SignUpUser struct {
	Username string `json:"username" validate:"required,min=4,max=20,alphanum"`
	Email    string `json:"email" validate:"required,email"`
}

type LogInUser struct {
	Username string `json:"username" validate:"required,min=4,max=20,alphanum"`
	Password string `json:"password" validate:"required"`
}

func generateRandomPassword() (password string, err error) {
	passwordBytes := make([]byte, passwordLength)
	_, err = rand.Read(passwordBytes)
	if err != nil {
		log.Printf("Error generating random password: %s", err)
		return
	}
	password = base64.URLEncoding.EncodeToString(passwordBytes)
	return
}

func generateSalt() (salt string, err error) {
	saltBytes := make([]byte, saltLength)
	_, err = rand.Read(saltBytes)
	if err != nil {
		log.Printf("Error generating salt: %s", err)
		return
	}
	salt = base64.URLEncoding.EncodeToString(saltBytes)
	return
}

func (s *Service) SignUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var user SignUpUser
	err := json.NewDecoder(r.Body).Decode(&user)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err = s.validator.Struct(user)
	if err != nil {
		for _, e := range err.(validator.ValidationErrors) {
			log.Printf("Error validating user: %s\n", e)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	password, err := generateRandomPassword()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	salt, err := generateSalt()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(password+salt), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Error hashing password: %s\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Convert the hashed password to a string
	hashString := string(hash)

	errs := s.db.InsertUser(user.Username, salt, hashString, user.Email)
	if len(errs) != 0 {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	msg := fmt.Sprintf(msgTemplate, user.Username, password)
	if err := s.sender.Send(
		user.Email,
		subject,
		msg,
	); err != nil {
		log.Printf("Error sending email: %s\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (s *Service) generateToken(username string) (signedToken string, err error) {
	expirationTime := time.Now().Add(20 * time.Minute)

	token := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"username": username,
		"exp":      expirationTime.Unix(),
	})

	signedToken, err = token.SignedString(s.signKey)
	return
}

func (s *Service) SignIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var user LogInUser
	err := json.NewDecoder(r.Body).Decode(&user)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err = s.validator.Struct(user)
	if err != nil {
		for _, e := range err.(validator.ValidationErrors) {
			log.Printf("Error validating user: %s\n", e)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	salt, hash, err := s.db.GetSaltAndHash(user.Username)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Compare the password with the hash
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(user.Password+salt))
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	token, err := s.generateToken(user.Username)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Authorization", "Bearer "+token)
	w.WriteHeader(http.StatusOK)
}

func (s *Service) AuthenticationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizationHeader := r.Header.Get("Authorization")
		if authorizationHeader == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		bearerToken := strings.Split(authorizationHeader, " ")
		if len(bearerToken) != 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		token, err := jwt.Parse(bearerToken[1], func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}

			// hmacSampleSecret is a []byte containing your secret, e.g. []byte("my_secret_key")
			return s.signKey, nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS512.Name}))
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if !token.Valid {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
