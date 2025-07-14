package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"main/db"
)

type GoogleUser struct {
	Id    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

var googleOauthConfig *oauth2.Config

func SetupGoogleOAuth() {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		fmt.Println("❌ 환경변수 GOOGLE_CLIENT_ID 또는 GOOGLE_CLIENT_SECRET 누락됨")
	}

	googleOauthConfig = &oauth2.Config{
		RedirectURL:  "http://localhost:8081/google/oauth2",
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes: []string{
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Endpoint: google.Endpoint,
	}
}

func GoogleLoginHandler(c *gin.Context) {
	url := googleOauthConfig.AuthCodeURL("state-token")
	c.Redirect(http.StatusTemporaryRedirect, url)
}

func GoogleCallbackHandler(c *gin.Context) {
	code := c.Query("code")

	token, err := googleOauthConfig.Exchange(context.Background(), code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "토큰 교환 실패", "detail": err.Error()})
		return
	}

	client := googleOauthConfig.Client(context.Background(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "사용자 정보 요청 실패", "detail": err.Error()})
		return
	}
	defer resp.Body.Close()

	var gu GoogleUser
	if err := json.NewDecoder(resp.Body).Decode(&gu); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "JSON 디코딩 실패", "detail": err.Error()})
		return
	}

	_, err = db.DB.Exec(`
        INSERT INTO google_user (id, email, name)
        VALUES ($1, $2, $3)
        ON CONFLICT (id) DO NOTHING
    `, gu.Id, gu.Email, gu.Name)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB 저장 실패", "detail": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "로그인 성공",
		"user":    gu,
	})
}
