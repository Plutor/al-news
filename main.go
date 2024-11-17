package main

import (
  "bytes"
  "encoding/json"
  "encoding/xml"
  "fmt"
  "log"
  "io"
  "math/rand"
  "net/http"
  "os"
  "path/filepath"
  "regexp"
  "strings"
  "time"
)

// AuthResponse represents the authentication response from Bluesky
type AuthResponse struct {
  AccessJwt string `json:"accessJwt"`
  Did       string `json:"did"`
}

// ErrorResponse represents the error response structure from Bluesky
type ErrorResponse struct {
  Error   string `json:"error"`
  Message string `json:"message"`
}

const (
  authURL = "https://bsky.social/xrpc/com.atproto.server.createSession"
  postURL = "https://bsky.social/xrpc/com.atproto.repo.createRecord"
  uploadImageURL = "https://bsky.social/xrpc/com.atproto.repo.uploadBlob"
)

// This weirdness is because I'm running on GCP Cloud Run
// TODO: Make this easier to run as one-off instead
func main() {
  // Start a server listening on port $PORT
  port := os.Getenv("PORT")
  if port == "" {
    port = "8080"
  }
  http.HandleFunc("/", CloudRunAlNewsPost)
  srv := http.Server{
    Addr: fmt.Sprintf(":%s", port),
  }
  log.Printf("Listening on %s", port)
  srv.ListenAndServe()
}

func CloudRunAlNewsPost(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(204)
  AlNewsPost()
}

func AlNewsPost() {
  // Only load .env file in development environment
  if os.Getenv("ENVIRONMENT") != "production" {
    err := godotenv.Load()
    if err != nil {
      log.Fatal("Error loading .env file")
    }
  }

  // Load environment variables
  username := os.Getenv("BLUESKY_USERNAME")
  if username == "" {
    log.Fatal("BLUESKY_USERNAME environment variable not set")
  }

  password := os.Getenv("BLUESKY_PASSWORD")
  if password == "" {
    log.Fatal("BLUESKY_PASSWORD environment variable not set")
  }

  // Authenticate and obtain access token
  authResponse, err := authenticate(username, password)
  if err != nil {
    log.Fatalf("Authentication failed: %v", err)
  }

  imageData, imageName, err := getImage()
  if err != nil {
    log.Fatalf("getImage() = %v", err)
  }

  postBody, err := getPostBody()
  if err != nil {
    log.Fatalf("getPostBody() = %v", err)
  }

  // Attach image a la https://docs.bsky.app/docs/advanced-guides/posts#images-embeds
  // Post image and message using access token
  log.Printf("Uploading %v byte image of %v", len(imageData), imageName)
  imageBlob, err := uploadImage(authResponse.AccessJwt, authResponse.Did, imageData)
  if err != nil {
    log.Fatalf("uploadImage() = %v", err)
  }
  log.Printf("Posting %q", postBody)
  err = postMessage(authResponse.AccessJwt, authResponse.Did, postBody, imageBlob, imageName)
  if err != nil {
    log.Fatalf("postMessage() = %v", err)
  }

  log.Println("Message posted successfully!")
}

func authenticate(identifier string, password string) (*AuthResponse, error) {
  authBody := map[string]string{
    "identifier": identifier,
    "password":   password,
  }
  bodyBytes, err := json.Marshal(authBody)
  if err != nil {
    return nil, fmt.Errorf("failed to marshal auth request body: %w", err)
  }

  req, err := http.NewRequest("POST", authURL, bytes.NewBuffer(bodyBytes))
  if err != nil {
    return nil, fmt.Errorf("failed to create auth request: %w", err)
  }
  req.Header.Set("Content-Type", "application/json")

  client := &http.Client{}
  resp, err := client.Do(req)
  if err != nil {
    return nil, fmt.Errorf("auth request failed: %w", err)
  }
  defer resp.Body.Close()

  if resp.StatusCode == http.StatusOK {
    var authResponse AuthResponse
    if err := json.NewDecoder(resp.Body).Decode(&authResponse); err != nil {
      return nil, fmt.Errorf("failed to decode auth response: %w", err)
    }

    log.Println("Authentication successful!")
    return &authResponse, nil
  }

  var errResponse ErrorResponse
  if err := json.NewDecoder(resp.Body).Decode(&errResponse); err != nil {
    return nil, fmt.Errorf("failed to decode error response: %w", err)
  }
  return nil, fmt.Errorf("auth error (%d): %s - %s", resp.StatusCode, errResponse.Error, errResponse.Message)
}

func postMessage(accessToken, did, message string, imageBlob map[string]interface{}, imageName string) error {
  postBody := map[string]interface{}{
    "repo":       did,
    "collection": "app.bsky.feed.post",
    "record": map[string]interface{}{
      "$type":     "app.bsky.feed.post",
      "text":      message,
      "createdAt": time.Now().UTC().Format(time.RFC3339),
      "embed": map[string]interface{}{
        "$type": "app.bsky.embed.images",
        "images": []map[string]interface{}{
            map[string]interface{}{
              "alt": imageName,
              "image": imageBlob,
            },
        },
      },
    },
  }
  bodyBytes, err := json.Marshal(postBody)
  if err != nil {
    return fmt.Errorf("failed to marshal post request body: %w", err)
  }
  log.Printf("Post JSON = %+v", string(bodyBytes))

  req, err := http.NewRequest("POST", postURL, bytes.NewBuffer(bodyBytes))
  if err != nil {
    return fmt.Errorf("failed to create post request: %w", err)
  }
  req.Header.Set("Authorization", "Bearer "+accessToken)
  req.Header.Set("Content-Type", "application/json")

  client := &http.Client{}
  resp, err := client.Do(req)
  if err != nil {
    return fmt.Errorf("post request failed: %w", err)
  }
  defer resp.Body.Close()

  if resp.StatusCode == http.StatusOK {
    log.Println("Post successful!")
    return nil
  }

  var errResponse ErrorResponse
  if err := json.NewDecoder(resp.Body).Decode(&errResponse); err != nil {
    return fmt.Errorf("failed to decode error response: %w", err)
  }
  return fmt.Errorf("post error (%d): %s - %s", resp.StatusCode, errResponse.Error, errResponse.Message)
}

func uploadImage(accessToken, did string, imageData []byte) (map[string]interface{}, error) {
  req, err := http.NewRequest("POST", uploadImageURL, bytes.NewBuffer(imageData))
  if err != nil {
    return nil, fmt.Errorf("failed to upload image: %w", err)
  }
  req.Header.Set("Authorization", "Bearer "+accessToken)
  req.Header.Set("Content-Type", "image/jpg")

  client := &http.Client{}
  resp, err := client.Do(req)
  if err != nil {
    return nil, fmt.Errorf("post request failed: %w", err)
  }
  defer resp.Body.Close()

  if resp.StatusCode == http.StatusOK {
    log.Println("Upload successful!")
    imageBlobResp := map[string]interface{}{}
    if err := json.NewDecoder(resp.Body).Decode(&imageBlobResp); err != nil {
      return nil, err
    }
    if blob, ok := imageBlobResp["blob"]; ok {
      if imageBlob, ok := blob.(map[string]interface{}); ok {
        return imageBlob, nil
      }
    }
    return nil, fmt.Errorf("No blob in response: %v", imageBlobResp)
  }

  var errResponse ErrorResponse
  if err := json.NewDecoder(resp.Body).Decode(&errResponse); err != nil {
    return nil, fmt.Errorf("failed to decode error response: %w", err)
  }
  return nil, fmt.Errorf("post error (%d): %s - %s", resp.StatusCode, errResponse.Error, errResponse.Message)
}

// =======================================================================

var sourcesRSS = []string {
  "https://www.sciencedaily.com/rss/computers_math/artificial_intelligence.xml", // Science Daily AI
  "https://feeds.a.dj.com/rss/RSSWSJD.xml", // WSJ Tech news
  "https://www.engadget.com/rss.xml", // Engadget
  "https://rss.nytimes.com/services/xml/rss/nyt/Technology.xml", // NYT tech
  "https://www.reutersagency.com/feed/?best-topics=tech&post_type=best", // Reuters tech
}
var aiRegex = regexp.MustCompile(`\b(AI|A\.I|Artificial Intelligence|artificial intelligence)\b`)

type Rss struct {
    Ch RssChannel `xml:"channel"`
}
type RssChannel struct {
    Item []RssItem `xml:"item"`
}
type RssItem struct {
    Title string `xml:"title"`
}

func getPostBody() (string, error) {
  aiTitles := []string{}
  for _, srcRSS := range sourcesRSS {
    // Get it
    resp, err := http.Get(srcRSS)
    if err != nil {
      log.Printf("http.Get(%q): %v", srcRSS, err)
      continue
    }
    defer resp.Body.Close()
    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
      log.Printf("io.ReadAll(%q): %v", srcRSS, err)
      continue
    }

    // Parse the xml
    var rss Rss
    if err := xml.Unmarshal(respBody, &rss); err != nil {
      log.Printf("xml.Unmarshal(%q): %v", srcRSS, err)
      continue
    }

    // Find a title that contains AI
    for _, item := range rss.Ch.Item {
      match := aiRegex.FindString(item.Title)
      if match == "" {
        continue
      }
      aiTitles = append(aiTitles, item.Title)
    }
  }

  log.Printf("Found %v matching possible titles", len(aiTitles))
  if len(aiTitles) == 0 {
    return "", fmt.Errorf("Found no AI titles somehow")
  }

  // Pick one at random
  // TODO: Dedup based on recently posted headlines
  title := aiTitles[rand.Intn(len(aiTitles))]
  return aiRegex.ReplaceAllLiteralString(title, "Al"), nil
}

func getImage() ([]byte, string, error) {
    images := []string{}
    err := filepath.Walk("./images", func(path string, info os.FileInfo, err error) error {
        if strings.HasSuffix(info.Name(), ".jpg") {
            images = append(images, path)
        }
        return nil
    })
    if err != nil {
      return nil, "", fmt.Errorf("filepath.Walk() = %v", err)
    }
    if len(images) == 0 {
      return nil, "", fmt.Errorf("Found no images")
    }
    
    imageFile := images[rand.Intn(len(images))]
    data, err := os.ReadFile(imageFile)
    return data, strings.Split(imageFile, ".")[0], err
}
