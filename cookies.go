/*
Copyright 2015 All rights reserved.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	uuid "github.com/gofrs/uuid"
	"github.com/gogatekeeper/gatekeeper/pkg/constant"
)

// dropCookie drops a cookie into the response
func (r *oauthProxy) dropCookie(wrt http.ResponseWriter, host, name, value string, duration time.Duration) {
	// step: default to the host header, else the config domain
	domain := ""

	if r.config.CookieDomain != "" {
		domain = r.config.CookieDomain
	}

	path := r.config.BaseURI

	if path == "" {
		path = "/"
	}

	cookie := &http.Cookie{
		Domain:   domain,
		HttpOnly: r.config.HTTPOnlyCookie,
		Name:     name,
		Path:     path,
		Secure:   r.config.SecureCookie,
		Value:    value,
	}

	if !r.config.EnableSessionCookies && duration != 0 {
		cookie.Expires = time.Now().Add(duration)
	}

	switch r.config.SameSiteCookie {
	case constant.SameSiteStrict:
		cookie.SameSite = http.SameSiteStrictMode
	case constant.SameSiteLax:
		cookie.SameSite = http.SameSiteLaxMode
	}

	http.SetCookie(wrt, cookie)
}

// maxCookieChunkSize calculates max cookie chunk size, which can be used for cookie value
func (r *oauthProxy) getMaxCookieChunkLength(req *http.Request, cookieName string) int {
	maxCookieChunkLength := 4069 - len(cookieName)

	if r.config.CookieDomain != "" {
		maxCookieChunkLength -= len(r.config.CookieDomain)
	} else {
		maxCookieChunkLength -= len(strings.Split(req.Host, ":")[0])
	}

	if r.config.HTTPOnlyCookie {
		maxCookieChunkLength -= len("HttpOnly; ")
	}

	if !r.config.EnableSessionCookies {
		maxCookieChunkLength -= len("Expires=Mon, 02 Jan 2006 03:04:05 MST; ")
	}

	switch r.config.SameSiteCookie {
	case constant.SameSiteStrict:
		maxCookieChunkLength -= len("SameSite=Strict ")
	case constant.SameSiteLax:
		maxCookieChunkLength -= len("SameSite=Lax ")
	}

	if r.config.SecureCookie {
		maxCookieChunkLength -= len("Secure")
	}

	return maxCookieChunkLength
}

// dropCookieWithChunks drops a cookie from the response, taking into account possible chunks
func (r *oauthProxy) dropCookieWithChunks(req *http.Request, wrt http.ResponseWriter, name, value string, duration time.Duration) {
	maxCookieChunkLength := r.getMaxCookieChunkLength(req, name)

	if len(value) <= maxCookieChunkLength {
		r.dropCookie(wrt, req.Host, name, value, duration)
	} else {
		// write divided cookies because payload is too long for single cookie
		r.dropCookie(wrt, req.Host, name, value[0:maxCookieChunkLength], duration)

		for idx := maxCookieChunkLength; idx < len(value); idx += maxCookieChunkLength {
			end := idx + maxCookieChunkLength

			if end > len(value) {
				end = len(value)
			}

			r.dropCookie(
				wrt,
				req.Host,
				name+"-"+strconv.Itoa(idx/maxCookieChunkLength),
				value[idx:end],
				duration,
			)
		}
	}
}

// dropAccessTokenCookie drops a access token cookie from the response
func (r *oauthProxy) dropAccessTokenCookie(req *http.Request, w http.ResponseWriter, value string, duration time.Duration) {
	r.dropCookieWithChunks(req, w, r.config.CookieAccessName, value, duration)
}

// dropRefreshTokenCookie drops a refresh token cookie from the response
func (r *oauthProxy) dropRefreshTokenCookie(req *http.Request, w http.ResponseWriter, value string, duration time.Duration) {
	r.dropCookieWithChunks(req, w, r.config.CookieRefreshName, value, duration)
}

// writeStateParameterCookie sets a state parameter cookie into the response
func (r *oauthProxy) writeStateParameterCookie(req *http.Request, wrt http.ResponseWriter) string {
	uuid, err := uuid.NewV4()

	if err != nil {
		wrt.WriteHeader(http.StatusInternalServerError)
	}

	requestURI := req.URL.RequestURI()

	if r.config.NoProxy && !r.config.NoRedirects {
		xReqURI := req.Header.Get("X-Forwarded-Uri")
		requestURI = xReqURI
	}

	encRequestURI := base64.StdEncoding.EncodeToString([]byte(requestURI))

	r.dropCookie(wrt, req.Host, r.config.CookieRequestURIName, encRequestURI, 0)
	r.dropCookie(wrt, req.Host, r.config.CookieOAuthStateName, uuid.String(), 0)

	return uuid.String()
}

// clearAllCookies is just a helper function for the below
func (r *oauthProxy) clearAllCookies(req *http.Request, w http.ResponseWriter) {
	r.clearAccessTokenCookie(req, w)
	r.clearRefreshTokenCookie(req, w)
}

// clearRefreshSessionCookie clears the session cookie
func (r *oauthProxy) clearRefreshTokenCookie(req *http.Request, wrt http.ResponseWriter) {
	r.dropCookie(wrt, req.Host, r.config.CookieRefreshName, "", -10*time.Hour)

	// clear divided cookies
	for idx := 1; idx < 600; idx++ {
		var _, err = req.Cookie(r.config.CookieRefreshName + "-" + strconv.Itoa(idx))

		if err == nil {
			r.dropCookie(
				wrt,
				req.Host,
				r.config.CookieRefreshName+"-"+strconv.Itoa(idx),
				"",
				-10*time.Hour,
			)
		} else {
			break
		}
	}
}

// clearAccessTokenCookie clears the session cookie
func (r *oauthProxy) clearAccessTokenCookie(req *http.Request, wrt http.ResponseWriter) {
	r.dropCookie(wrt, req.Host, r.config.CookieAccessName, "", -10*time.Hour)

	// clear divided cookies
	for idx := 1; idx < len(req.Cookies()); idx++ {
		var _, err = req.Cookie(r.config.CookieAccessName + "-" + strconv.Itoa(idx))

		if err == nil {
			r.dropCookie(
				wrt,
				req.Host,
				r.config.CookieAccessName+"-"+strconv.Itoa(idx),
				"",
				-10*time.Hour,
			)
		} else {
			break
		}
	}
}
