package cgiserver

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/r0123r/fastcgi-serve/fcgiclient"
)

type CgiHandler struct {
	http.Handler
	Root       string
	DefaultApp string
	UseLangMap bool
	LangMap    map[string]string
	FcgiPort   int
	ServeFcgi  bool
	FcgiUnix   string
}

func CgiServer() *CgiHandler {
	path, _ := filepath.Abs(".")
	return &CgiHandler{nil, path, "", true, map[string]string{}, 3333, false, ""}
}

func (h *CgiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	var isCGI bool
	file := filepath.FromSlash(path)
	if len(file) > 0 && os.IsPathSeparator(file[len(file)-1]) {
		file = file[:len(file)-1]
	}
	ext := filepath.Ext(file)
	bin, isCGI := h.LangMap[ext]
	file = filepath.Join(h.Root, file)

	f, e := os.Stat(file)
	if e != nil || f.IsDir() {
		if len(h.DefaultApp) > 0 {
			file = h.DefaultApp
		}
		ext := filepath.Ext(file)
		bin, isCGI = h.LangMap[ext]
	}

	if isCGI {
		if !h.ServeFcgi {
			var cgih *cgi.Handler
			if h.UseLangMap {
				cgih = &cgi.Handler{
					Path: bin,
					Dir:  h.Root,
					//Root: h.Root,
					Args: []string{file},
					Env: []string{
						"SCRIPT_FILENAME=" + file,
						"SCRIPT_NAME=" + path,
						"DOCUMENT_ROOT=" + h.Root,
					},
				}
			} else {
				cgih = &cgi.Handler{
					Path: file,
					Root: h.Root,
				}
			}
			cgih.ServeHTTP(w, r)
		} else {
			h.FcgiHandler(w, r)
		}
	} else {
		if (f != nil && f.IsDir()) || file == "" {
			tmp := filepath.Join(file, "index.html")
			f, e = os.Stat(tmp)
			if e == nil {
				file = tmp
			}
		}
		http.ServeFile(w, r, file)
	}
}
func (h *CgiHandler) FcgiHandler(w http.ResponseWriter, r *http.Request) {
	reqParams := ""
	var filename string
	var scriptName string

	if r.Method == "POST" {
		body, _ := ioutil.ReadAll(r.Body)
		reqParams = string(body)
	}

	scriptName = r.URL.Path
	filename = filepath.Join(h.Root, scriptName)
	env := make(map[string]string)

	env["REQUEST_METHOD"] = r.Method
	env["SCRIPT_FILENAME"] = filename
	env["SCRIPT_NAME"] = scriptName
	env["SERVER_SOFTWARE"] = "go / fcgiclient "
	env["REMOTE_ADDR"] = r.RemoteAddr
	env["SERVER_PROTOCOL"] = "HTTP/1.1"
	env["PATH_INFO"] = r.URL.Path
	env["DOCUMENT_ROOT"] = h.Root
	env["QUERY_STRING"] = r.URL.RawQuery
	env["REQUEST_URI"] = r.URL.Path + "?" + r.URL.RawQuery
	env["HTTP_HOST"] = r.Host
	
	if r.ContentLength > 0 {
		env["CONTENT_LENGTH"] = fmt.Sprint(r.ContentLength)
	}
	if ctype := r.Header.Get("Content-Type"); ctype != "" {
		env["CONTENT_TYPE"] = ctype
	}

	for k, v := range r.Header {
		k = strings.Map(upperCaseAndUnderscore, k)
		if k == "PROXY" {
			// See Issue 16405
			continue
		}
		joinStr := ", "
		if k == "COOKIE" {
			joinStr = "; "
		}
		env["HTTP_"+k] = strings.Join(v, joinStr)
	}
	var adr interface{}
	if runtime.GOOS == "linux" && len(h.FcgiUnix) > 0 {
		adr = h.FcgiUnix
	} else {
		adr = h.FcgiPort
	}
	fcgi, err := fcgiclient.New("", adr)
	if err != nil {
		log.Print(err)
	}

	//fmt.Printf("%+v\n", env)
	content, _, err := fcgi.Request(env, reqParams)

	if err != nil {
		log.Print(err)
	}

	statusCode, headers, body, err := h.ParseFastCgiResponse(fmt.Sprintf("%s", content))

	for header, value := range headers {
		w.Header().Set(header, value)
	}
	w.WriteHeader(statusCode)
	fmt.Fprintf(w, "%s", body)

	//fmt.Printf("%s %v\n", r.URL, headers)
}

func (h *CgiHandler) ParseFastCgiResponse(content string) (int, map[string]string, string, error) {
	headers := make(map[string]string)

	parts := strings.SplitN(content, "\r\n\r\n", 2)

	if len(parts) < 2 {
		return 502, headers, "", errors.New("Cannot parse FastCGI Response")
	}
	headerParts := strings.Split(parts[0], "\n")
	//log.Print(headerParts)
	body := parts[1]
	status := 200

	if strings.HasPrefix(headerParts[0], "Status:") {
		lineParts := strings.SplitN(headerParts[0], " ", 3)
		status, _ = strconv.Atoi(lineParts[1])
	}

	for _, line := range headerParts {
		lineParts := strings.SplitN(line, ":", 2)

		if len(lineParts) < 2 {
			continue
		}

		lineParts[1] = strings.TrimSpace(lineParts[1])

		if lineParts[0] == "Status" {
			continue
		}

		headers[lineParts[0]] = lineParts[1]
	}

	return status, headers, body, nil
}
func upperCaseAndUnderscore(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z':
		return r - ('a' - 'A')
	case r == '-':
		return '_'
	case r == '=':
		// Maybe not part of the CGI 'spec' but would mess up
		// the environment in any case, as Go represents the
		// environment as a slice of "key=value" strings.
		return '_'
	}
	// TODO: other transformations in spec or practice?
	return r
}
