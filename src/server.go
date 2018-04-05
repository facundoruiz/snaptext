package server

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/cihub/seelog"
	"github.com/gin-contrib/multitemplate"
	"github.com/gin-gonic/gin"
)

var hubs map[string]*Hub

func init() {
	os.MkdirAll("data", 0755)
}

// Run will run the main program
func Run(port string) (err error) {
	defer log.Flush()

	hubs = make(map[string]*Hub)
	go func() {
		for {
			time.Sleep(1 * time.Second)
			namesToDelete := make(map[string]struct{})
			for name := range hubs {
				// log.Debugf("hub %s has %d clients", name, len(hubs[name].clients))
				if len(hubs[name].clients) == 0 {
					namesToDelete[name] = struct{}{}
					hubs[name].deleted = true
				}
			}
			for name := range namesToDelete {
				log.Debugf("deleting hub for %s", name)
				delete(hubs, name)
			}
		}
	}()

	// setup gin server
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Standardize logs
	r.HTMLRender = loadTemplates("index.html")
	r.Use(middleWareHandler(), gin.Recovery())
	r.HEAD("/", func(c *gin.Context) { // handler for the uptime robot
		c.String(http.StatusOK, "OK")
	})
	r.GET("/*name", func(c *gin.Context) {
		name := strings.ToLower(c.Param("name"))
		if len(name) == 1 {
			c.HTML(http.StatusOK, "index.html", gin.H{
				"Name": name[1:],
				"Form": true,
				"To":   c.DefaultQuery("to", ""),
				"From": c.DefaultQuery("from", ""),
			})
		} else if name == "/ws" {
			name = c.DefaultQuery("name", "")
			if name == "" {
				c.String(http.StatusOK, "OK")
				return
			}
			if _, ok := hubs[name]; !ok {
				hubs[name] = newHub(name)
				go hubs[name].run()
				time.Sleep(50 * time.Millisecond)
			}
			hubs[name].serveWs(c.Writer, c.Request)
		} else if strings.Contains(name, "/static") {
			filename := name[1:]
			var data []byte
			var errAssset error
			data, errAssset = Asset(filename)
			if errAssset != nil {
				c.String(http.StatusInternalServerError, "Could not find data")
			}
			c.Data(http.StatusOK, contentType(filename), data)
			return
		} else {
			m, _ := getNextMessage(strings.ToLower(name[1:]))
			c.HTML(http.StatusOK, "index.html", gin.H{
				"Name":         name[1:],
				"Form":         false,
				"Message":      template.HTML(m.Message),
				"Submessage":   template.HTML(m.Submessage),
				"MoreMessages": m.Meta == "more messages",
			})
		}
	})
	r.POST("/", handlerPostMessage) // handle for posting message
	log.Infof("Running at http://0.0.0.0:" + port)
	err = r.Run(":" + port)
	return
}

func handlerPostMessage(c *gin.Context) {
	message, err := func(c *gin.Context) (message string, err error) {
		var m messageJSON
		err = c.ShouldBindJSON(&m)
		if err != nil {
			err = errors.New("message, sender, or recipient cannot be empty")
			return
		}
		m, err = validateMessage(m)
		message = fmt.Sprintf("Sent. Message available at /%s", m.To)
		if err != nil {
			return
		}
		db := open(m.To)
		err = db.saveMessage(m)
		if err != nil {
			log.Error(err)
		}
		db.close()
		if _, ok := hubs[m.To]; ok {
			hubs[m.To].broadcastNextMessage(false)
		}
		return
	}(c)
	if err != nil {
		log.Warn(err)
		message = err.Error()
	}
	c.JSON(http.StatusOK, gin.H{
		"message": message,
		"success": err == nil,
	})
}

func middleWareHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		t := time.Now()
		// Add base headers
		addCORS(c)
		// Run next function
		c.Next()
		// Log request
		log.Infof("%v %v %v %s", c.Request.RemoteAddr, c.Request.Method, c.Request.URL, time.Since(t))
	}
}

func addCORS(c *gin.Context) {
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	c.Writer.Header().Set("Access-Control-Max-Age", "86400")
	c.Writer.Header().Set("Access-Control-Allow-Methods", "GET")
	c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-Max")
	c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
}

func contentType(filename string) string {
	switch {
	case strings.Contains(filename, ".css"):
		return "text/css"
	case strings.Contains(filename, ".jpg"):
		return "image/jpeg"
	case strings.Contains(filename, ".png"):
		return "image/png"
	case strings.Contains(filename, ".js"):
		return "application/javascript"
	case strings.Contains(filename, ".xml"):
		return "application/xml"
	}
	return "text/html"
}

func loadTemplates(list ...string) multitemplate.Render {
	r := multitemplate.New()

	for _, x := range list {
		templateString, err := Asset("templates/" + x)
		if err != nil {
			panic(err)
		}

		tmplMessage, err := template.New(x).Parse(string(templateString))
		if err != nil {
			panic(err)
		}

		r.Add(x, tmplMessage)
	}

	return r
}
