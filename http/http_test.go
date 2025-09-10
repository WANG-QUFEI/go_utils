package http

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/require"
)

var testServer *httptest.Server

var greetings = make(map[string]string)

type greetingMessage struct {
	Name     string `json:"name" xml:"name"`
	Greeting string `json:"greeting" xml:"greeting"`
}

func init() {
	router := httprouter.New()

	router.GET("/hello/:name", func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		availableRespTypes := []string{"text/plain", "application/json", "application/xml"}
		accept := r.Header.Get("Accept")
		if accept == "" {
			http.Error(w, "'Accept' request header is required", http.StatusBadRequest)
			return
		}
		if !slices.Contains(availableRespTypes, accept) {
			http.Error(w, fmt.Sprintf("Unsupported Accept header: '%s'", accept), http.StatusNotAcceptable)
			return
		}

		name := ps.ByName("name")
		if greeting, ok := greetings[name]; ok {
			switch accept {
			case "text/plain":
				w.Header().Set("Content-Type", "text/plain")
				_, _ = fmt.Fprintf(w, "%s: %s", name, greeting)
				return
			case "application/xml":
				w.Header().Set("Content-Type", "application/xml")
				_ = xml.NewEncoder(w).Encode(greetingMessage{name, greeting})
				return
			case "application/json":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(greetingMessage{name, greeting})
				return
			}
		}

		http.Error(w, "Name not found", http.StatusNotFound)
	})

	router.HEAD("/hello/:name", func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		name := ps.ByName("name")
		if v, ok := greetings[name]; ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(v)))
			w.Header().Set("ETag", uuid.New().String())
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "Name not found", http.StatusNotFound)
		}
	})

	router.PUT("/hello/:name", func(writer http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		accept := r.Header.Get("Accept")
		if accept == "" {
			http.Error(writer, "'Accept' request header is required", http.StatusBadRequest)
			return
		}
		if accept != "application/json" {
			http.Error(writer, fmt.Sprintf("Unsupported Accept header: '%s'", accept), http.StatusNotAcceptable)
			return
		}

		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			http.Error(writer, "'Content-Type' request header is required", http.StatusBadRequest)
			return
		}

		name := ps.ByName("name")
		switch {
		case contentType == "application/x-www-form-urlencoded":
			if err := r.ParseForm(); err != nil {
				http.Error(writer, "Failed to parse form data", http.StatusBadRequest)
				return
			}

			greeting := r.PostForm.Get("greeting")
			if greeting == "" {
				http.Error(writer, "'greeting' form value is required", http.StatusBadRequest)
				return
			}

			greetings[name] = greeting
			writer.WriteHeader(http.StatusNoContent)
			return
		case contentType == "application/json":
			var msg greetingMessage
			if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
				http.Error(writer, "Failed to decode JSON body", http.StatusBadRequest)
				return
			}

			if msg.Greeting == "" {
				http.Error(writer, "'greeting' field in JSON body is required", http.StatusBadRequest)
				return
			}

			greetings[name] = msg.Greeting
			writer.WriteHeader(http.StatusNoContent)
			return
		case strings.HasPrefix(contentType, "multipart/form-data"):
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				http.Error(writer, fmt.Sprintf("Failed to parse multipart form data: %v", err), http.StatusBadRequest)
				return
			}

			greeting := r.FormValue("greeting")
			if greeting == "" {
				http.Error(writer, "'greeting' form value is required", http.StatusBadRequest)
				return
			}

			greetings[name] = greeting
			writer.WriteHeader(http.StatusNoContent)
			return
		default:
			http.Error(writer, fmt.Sprintf("Unsupported Content-Type: '%s'", contentType), http.StatusUnsupportedMediaType)
			return
		}
	})

	router.POST("/hello", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			http.Error(w, "'Content-Type' request header is required", http.StatusBadRequest)
			return
		}
		if contentType != "application/json" {
			http.Error(w, fmt.Sprintf("Only Content-Type 'application/json' is supported, requested: '%s'", contentType),
				http.StatusUnsupportedMediaType)
			return
		}

		var content []greetingMessage
		if err := json.NewDecoder(r.Body).Decode(&content); err != nil {
			http.Error(w, "Failed to decode JSON body", http.StatusBadRequest)
			return
		}

		greetings = make(map[string]string)
		allProcessed := true
		for _, msg := range content {
			if msg.Name == "" || msg.Greeting == "" {
				allProcessed = false
				continue
			}
			greetings[msg.Name] = msg.Greeting
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"all-processed": allProcessed,
		})
	})

	router.PATCH("/hello/:name", func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		name := ps.ByName("name")
		if _, ok := greetings[name]; !ok {
			http.Error(w, "Name not found", http.StatusNotFound)
			return
		}

		var msg greetingMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, "Failed to decode JSON body", http.StatusBadRequest)
			return
		}
		if msg.Greeting == "" {
			http.Error(w, "'greeting' field in JSON body is required", http.StatusBadRequest)
			return
		}

		greetings[name] = msg.Greeting
		w.WriteHeader(http.StatusNoContent)
	})

	router.DELETE("/hello/:name", func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		name := ps.ByName("name")
		if _, ok := greetings[name]; ok {
			delete(greetings, name)
			w.WriteHeader(http.StatusOK)
			return
		}

		http.Error(w, "Name not found", http.StatusNotFound)
	})

	router.OPTIONS("/hello/:name", func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		name := ps.ByName("name")
		if _, ok := greetings[name]; ok {
			w.Header().Set("Allow", "GET, HEAD, PUT, PATCH, DELETE, OPTIONS")
		} else {
			w.Header().Set("Allow", "PUT")
		}
		w.WriteHeader(http.StatusOK)
	})

	testServer = httptest.NewServer(router)
}

func TestHttpGet(t *testing.T) {
	httpGetUnexpectedStatusCode(t)
	httpGetPlainText(t)
	httpGetJSON(t)
	httpGetXML(t)
}

func httpGetUnexpectedStatusCode(t *testing.T) {
	u, err := url.Parse(testServer.URL + "/hello/John")
	if err != nil {
		t.Fatal(err)
	}

	req := Request[greetingMessage]{
		URL:            u,
		Method:         http.MethodGet,
		AcceptedStatus: []int{http.StatusOK, http.StatusNotFound},
		Decoder: func(content []byte, statusCode int) (*greetingMessage, error) {
			panic("will not be called")
		},
	}

	_, err = req.DoHttpRequest(context.Background(), nil)
	require.NotNil(t, err)
	t.Log("expected error:", err)

	req.Headers = http.Header{
		"Accept": []string{"text/html"},
	}
	_, err = req.DoHttpRequest(context.Background(), nil)
	require.NotNil(t, err)
	t.Log("expected error:", err)
}

func httpGetPlainText(t *testing.T) {
	u, err := url.Parse(testServer.URL + "/hello/Anna")
	if err != nil {
		t.Fatal(err)
	}

	greetings["Anna"] = "You're smart"

	req := Request[string]{
		URL:            u,
		Method:         http.MethodGet,
		Headers:        http.Header{"Accept": []string{"text/plain"}},
		AcceptedStatus: []int{http.StatusOK, http.StatusNotFound},
		Decoder: func(content []byte, statusCode int) (*string, error) {
			switch statusCode {
			case http.StatusOK:
				result := string(content)
				return &result, nil
			case http.StatusNotFound:
				return nil, nil
			default:
				return nil, fmt.Errorf("unexpected status code: %d", statusCode)
			}
		},
	}

	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Nil(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, resp.Data)
	require.Equal(t, "Anna: You're smart", *resp.Data)
}

func httpGetJSON(t *testing.T) {
	u, err := url.Parse(testServer.URL + "/hello/Anna")
	if err != nil {
		t.Fatal(err)
	}

	greetings["Anna"] = "You're smart"

	req := Request[greetingMessage]{
		URL:            u,
		Method:         http.MethodGet,
		Headers:        http.Header{"Accept": []string{"application/json"}},
		AcceptedStatus: []int{http.StatusOK, http.StatusNotFound},
		Decoder:        DefaultJSONDecoder[greetingMessage],
	}

	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Nil(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, resp.Data)
	require.Equal(t, "Anna", resp.Data.Name)
	require.Equal(t, "You're smart", resp.Data.Greeting)
}

func httpGetXML(t *testing.T) {
	u, err := url.Parse(testServer.URL + "/hello/Anna")
	if err != nil {
		t.Fatal(err)
	}

	greetings["Anna"] = "You're smart"

	req := Request[greetingMessage]{
		URL:            u,
		Method:         http.MethodGet,
		Headers:        http.Header{"Accept": []string{"application/xml"}},
		AcceptedStatus: []int{http.StatusOK, http.StatusNotFound},
		Decoder:        DefaultXMLDecoder[greetingMessage],
	}

	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Nil(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, resp.Data)
	require.Equal(t, "Anna", resp.Data.Name)
	require.Equal(t, "You're smart", resp.Data.Greeting)
}

func TestHttpHead(t *testing.T) {
	u, err := url.Parse(testServer.URL + "/hello/Charlie")
	if err != nil {
		t.Fatal(err)
	}

	praise := "You consistently deliver clean, efficient, and innovative code, demonstrating exceptional skill as a software developer"
	greetings["Charlie"] = praise
	req := Request[Null]{
		URL:    u,
		Method: http.MethodHead,
	}
	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Nil(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, resp.Body)
	require.NotEmpty(t, resp.Headers.Get("Content-Type"))
	require.Equal(t, "application/json", resp.Headers.Get("Content-Type"))
	require.Equal(t, fmt.Sprintf("%d", len(praise)), resp.Headers.Get("Content-Length"))
	require.NotEmpty(t, resp.Headers.Get("ETag"))
}

func TestHttpPut(t *testing.T) {
	httpPutWithJSON(t)
	httpPutWithMultipartForm(t)
	httpPutWithFormURLEncoded(t)
}

func httpPutWithJSON(t *testing.T) {
	u, err := url.Parse(testServer.URL + "/hello/Bob")
	if err != nil {
		t.Fatal(err)
	}

	body := greetingMessage{
		Greeting: "You're cool",
	}

	req := Request[Null]{
		URL:    u,
		Method: http.MethodPut,
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
			"Accept":       []string{"application/json"},
		},
		Body:    body,
		Encoder: DefaultJSONEncoder,
	}

	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Nil(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Empty(t, resp.Body)
	require.Equal(t, greetings["Bob"], "You're cool")
}

func httpPutWithMultipartForm(t *testing.T) {
	u, err := url.Parse(testServer.URL + "/hello/Bob")
	if err != nil {
		t.Fatal(err)
	}

	body := []MultipartFormField{
		{
			Type: FieldType,
			Name: "greeting",
			Data: []byte("You're awesome"),
		},
	}

	var boundary string
	encoder, headerCallback := DefaultMultipartFormDataEncoder(&boundary)
	req := Request[Null]{
		URL:    u,
		Method: http.MethodPut,
		Headers: http.Header{
			"Accept": []string{"application/json"},
		},
		HeaderCallback: headerCallback,
		Body:           body,
		Encoder:        encoder,
	}

	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Nil(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Empty(t, resp.Body)
	require.Equal(t, greetings["Bob"], "You're awesome")
}

func httpPutWithFormURLEncoded(t *testing.T) {
	u, err := url.Parse(testServer.URL + "/hello/Bob")
	if err != nil {
		t.Fatal(err)
	}

	body := url.Values{
		"greeting": []string{"You're great"},
	}

	req := Request[Null]{
		URL:    u,
		Method: http.MethodPut,
		Headers: http.Header{
			"Accept":       []string{"application/json"},
			"Content-Type": []string{"application/x-www-form-urlencoded"},
		},
		Body:    body,
		Encoder: DefaultFormURLEncodedEncoder,
	}

	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Nil(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Empty(t, resp.Body)
	require.Equal(t, greetings["Bob"], "You're great")
}

func TestHttpPost(t *testing.T) {
	u, err := url.Parse(testServer.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}

	body := []greetingMessage{
		{
			Name:     "Name1",
			Greeting: "You're smart",
		},
		{
			Name:     "Name2",
			Greeting: "You're awesome",
		},
		{
			Name:     "Name3",
			Greeting: "You're great",
		},
	}

	req := Request[map[string]bool]{
		URL:    u,
		Method: http.MethodPost,
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
			"Accept":       []string{"application/json"},
		},
		Body:    body,
		Encoder: DefaultJSONEncoder,
		Decoder: DefaultJSONDecoder[map[string]bool],
	}

	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Nil(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, resp.Data)
	require.True(t, (*resp.Data)["all-processed"])

	require.Equal(t, greetings["Name1"], "You're smart")
	require.Equal(t, greetings["Name2"], "You're awesome")
	require.Equal(t, greetings["Name3"], "You're great")
}

func TestHttpPatch(t *testing.T) {
	greetings = make(map[string]string)
	u, err := url.Parse(testServer.URL + "/hello/David")
	if err != nil {
		t.Fatal(err)
	}

	req := Request[[]byte]{
		URL:    u,
		Method: http.MethodPatch,
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
			"Accept":       []string{"application/json"},
		},
		Body: greetingMessage{
			Greeting: "You're fantastic",
		},
		Encoder: DefaultJSONEncoder,
	}

	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, resp)
	t.Log("expected error:", err)

	greetings["David"] = "You're cool"
	resp, err = req.DoHttpRequest(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Body, 0)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, greetings["David"], "You're fantastic")
}

func TestHttpDelete(t *testing.T) {
	greetings = make(map[string]string)
	u, err := url.Parse(testServer.URL + "/hello/Eve")
	if err != nil {
		t.Fatal(err)
	}

	req := Request[[]byte]{
		URL:    u,
		Method: http.MethodDelete,
	}
	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, resp)
	t.Log("expected error:", err)

	greetings["Eve"] = "You're amazing"
	resp, err = req.DoHttpRequest(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Body, 0)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, ok := greetings["Eve"]
	require.False(t, ok)
}

func TestHttpOptions(t *testing.T) {
	greetings = map[string]string{
		"Frank": "You're incredible",
	}

	u, err := url.Parse(testServer.URL + "/hello/Frank")
	if err != nil {
		t.Fatal(err)
	}

	req := Request[[]byte]{
		URL:    u,
		Method: http.MethodOptions,
	}
	resp, err := req.DoHttpRequest(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Body, 0)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "GET, HEAD, PUT, PATCH, DELETE, OPTIONS", resp.Headers.Get("Allow"))

	delete(greetings, "Frank")
	resp, err = req.DoHttpRequest(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Body, 0)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "PUT", resp.Headers.Get("Allow"))
}
