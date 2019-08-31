// Copyright 2018 tree xie
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package elton

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/vicanso/hes"
)

const (
	// StatusRunning running status
	StatusRunning = iota
	// StatusClosing closing status
	StatusClosing
	// StatusClosed closed status
	StatusClosed
)

var (
	// privateIPBlocks private ip blocks
	privateIPBlocks []*net.IPNet
)

type (
	// Skipper check for skip middleware
	Skipper func(c *Context) bool
	// Validator validate function for param
	Validator func(value string) error
	// RouterInfo router's info
	RouterInfo struct {
		Method string `json:"method,omitempty"`
		Path   string `json:"path,omitempty"`
	}
	// Elton web framework instance
	Elton struct {
		// status of elton
		status int32
		// Server http server
		Server *http.Server
		// Router http router
		Router *httprouter.Router
		// Routers all router infos
		Routers []*RouterInfo
		// Middlewares middleware function
		Middlewares []Handler
		// PreMiddlewares pre middleware function
		PreMiddlewares []PreHandler
		errorListeners []ErrorListener
		traceListeners []TraceListener
		// ErrorHandler set the function for error handler
		ErrorHandler ErrorHandler
		// NotFoundHandler set the function for not found handler
		NotFoundHandler http.HandlerFunc
		// GenerateID generate id function, will use it for create id for context
		GenerateID GenerateID
		// EnableTrace enable trace
		EnableTrace bool
		// SignedKeys signed keys
		SignedKeys SignedKeysGenerator
		// functionInfos the function address:name map
		functionInfos map[uintptr]string
		ctxPool       sync.Pool
		validators    map[string]Validator
	}
	// TraceInfo trace's info
	TraceInfo struct {
		Name     string        `json:"name,omitempty"`
		Duration time.Duration `json:"duration,omitempty"`
	}
	// TraceInfos trace infos
	TraceInfos []*TraceInfo
	// Router router
	Router struct {
		Method     string    `json:"method,omitempty"`
		Path       string    `json:"path,omitempty"`
		HandleList []Handler `json:"-"`
	}
	// Group group router
	Group struct {
		Path        string
		HandlerList []Handler
		routers     []*Router
	}
	// ErrorHandler error handle function
	ErrorHandler func(*Context, error)
	// GenerateID generate context id
	GenerateID func() string
	// Handler elton handle function
	Handler func(*Context) error
	// ErrorListener error listener function
	ErrorListener func(*Context, error)
	// TraceListener trace listener
	TraceListener func(*Context, TraceInfos)
	// PreHandler pre handler
	PreHandler func(*http.Request)
)

// DefaultSkipper default skipper function(not skip)
func DefaultSkipper(c *Context) bool {
	return c.Committed
}

// https://stackoverflow.com/questions/43274579/golang-check-if-ip-address-is-in-a-network/43274687
func initPrivateIPBlocks() {
	for _, cidr := range []string{
		"127.0.0.0/8",    // IPv4 loopback
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"::1/128",        // IPv6 loopback
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique local addr
	} {
		_, block, _ := net.ParseCIDR(cidr)
		privateIPBlocks = append(privateIPBlocks, block)
	}
}

func init() {
	initPrivateIPBlocks()
}

// IsPrivateIP check the ip is private
func IsPrivateIP(ip net.IP) bool {
	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// New create a elton instance
func New() *Elton {
	d := NewWithoutServer()
	s := &http.Server{
		Handler: d,
	}
	d.Server = s
	return d
}

// NewWithoutServer create a elton instance without server
func NewWithoutServer() *Elton {
	d := &Elton{
		Router:        httprouter.New(),
		functionInfos: make(map[uintptr]string),
	}
	d.ctxPool.New = func() interface{} {
		return &Context{}
	}
	return d
}

// NewGroup new group
func NewGroup(path string, handlerList ...Handler) *Group {
	return &Group{
		Path:        path,
		HandlerList: handlerList,
	}
}

// SetFunctionName set function name
func (d *Elton) SetFunctionName(fn interface{}, name string) {
	p := reflect.ValueOf(fn).Pointer()
	d.functionInfos[p] = name
}

// GetFunctionName get function name
func (d *Elton) GetFunctionName(fn interface{}) string {
	p := reflect.ValueOf(fn).Pointer()
	name := d.functionInfos[p]
	if name != "" {
		return name
	}
	return runtime.FuncForPC(p).Name()
}

// ListenAndServe listen and serve for http server
func (d *Elton) ListenAndServe(addr string) error {
	if d.Server == nil {
		panic(errors.New("server is not initialized"))
	}
	d.Server.Addr = addr
	return d.Server.ListenAndServe()
}

// Serve serve for http server
func (d *Elton) Serve(l net.Listener) error {
	if d.Server == nil {
		panic(errors.New("server is not initialized"))
	}
	return d.Server.Serve(l)
}

// Close close the http server
func (d *Elton) Close() error {
	return d.Server.Close()
}

// GracefulClose graceful close the http server
func (d *Elton) GracefulClose(delay time.Duration) error {
	atomic.StoreInt32(&d.status, StatusClosing)
	time.Sleep(delay)
	atomic.StoreInt32(&d.status, StatusClosed)
	return d.Close()
}

// GetStatus get status of elton
func (d *Elton) GetStatus() int32 {
	return atomic.LoadInt32(&d.status)
}

// ServeHTTP http handler
func (d *Elton) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	status := atomic.LoadInt32(&d.status)
	// 非运行中的状态
	if status != StatusRunning {
		resp.WriteHeader(http.StatusServiceUnavailable)
		resp.Write([]byte(fmt.Sprintf("service is not available, status is %d", status)))
		return
	}
	for _, preHandler := range d.PreMiddlewares {
		preHandler(req)
	}
	fn, params, _ := d.Router.Lookup(req.Method, req.URL.Path)
	if fn != nil {
		fn(resp, req, params)
		return
	}
	// 404处理
	d.NotFound(resp, req)
}

// fillContext fill the context
func (d *Elton) fillContext(c *Context, resp http.ResponseWriter, req *http.Request) {
	c.Request = req
	c.Response = resp
	if resp != nil {
		c.Headers = resp.Header()
	}
}

// Handle add http handle function
func (d *Elton) Handle(method, path string, handlerList ...Handler) {
	for _, fn := range handlerList {
		name := d.GetFunctionName(fn)
		d.SetFunctionName(fn, name)
	}

	if d.Routers == nil {
		d.Routers = make([]*RouterInfo, 0)
	}
	d.Routers = append(d.Routers, &RouterInfo{
		Method: method,
		Path:   path,
	})
	d.Router.Handle(method, path, func(resp http.ResponseWriter, req *http.Request, params httprouter.Params) {
		c := d.ctxPool.Get().(*Context)
		c.Reset()
		d.fillContext(c, resp, req)
		c.RawParams = params
		if len(params) != 0 {
			c.Params = make(map[string]string)
			for _, item := range params {
				c.Params[item.Key] = item.Value
			}
		}

		if d.GenerateID != nil {
			c.ID = d.GenerateID()
		}
		c.Route = path
		c.elton = d
		mids := d.Middlewares
		maxMid := len(mids)
		maxNext := maxMid + len(handlerList)
		index := -1
		var traceInfos TraceInfos
		if d.EnableTrace {
			traceInfos = make(TraceInfos, 0, maxNext)
		}
		c.Next = func() error {
			// 如果已设置响应数据，则不再执行后续的中间件
			if c.Committed {
				return nil
			}
			index++
			var fn Handler
			// 如果调用过多的next，则直接返回
			if index >= maxNext {
				return nil
			}

			// 在最后一个handler执行时，如果有配置参数校验，则校验
			if index == maxNext-1 && d.validators != nil {
				for key, value := range c.Params {
					if d.validators[key] != nil {
						e := d.validators[key](value)
						if e != nil {
							return e
						}
					}
				}
			}
			// 如果已执行完公共添加的中间件，执行handler list
			if index >= maxMid {
				fn = handlerList[index-maxMid]
			} else {
				fn = mids[index]
			}
			if traceInfos == nil {
				return fn(c)
			}
			fnName := d.GetFunctionName(fn)
			// 如果函数名字为 - ，则跳过
			if fnName == "-" {
				return fn(c)
			}
			startedAt := time.Now()

			traceInfo := &TraceInfo{
				Name: fnName,
			}
			// 先添加至slice中，保证顺序
			traceInfos = append(traceInfos, traceInfo)
			err := fn(c)
			// 完成后计算时长（前面的中间件包括后面中间件的处理时长）
			traceInfo.Duration = time.Since(startedAt)
			return err
		}
		err := c.Next()
		if traceInfos != nil {
			max := len(traceInfos)
			for i, traceInfo := range traceInfos {
				if i < max-1 {
					// 计算真实耗时（不包括后面中间件处理时长）
					traceInfo.Duration -= traceInfos[i+1].Duration
				}
			}
			d.EmitTrace(c, traceInfos)
		}
		if err != nil {
			d.EmitError(c, err)
		}
		// 如果已commit 表示返回数据已设置，无需处理
		if !c.Committed {
			if err != nil {
				d.Error(c, err)
			} else {
				if c.BodyBuffer != nil {
					c.SetHeader(HeaderContentLength, strconv.Itoa(c.BodyBuffer.Len()))
				}
				if c.StatusCode != 0 {
					resp.WriteHeader(c.StatusCode)
				}
				if c.BodyBuffer != nil {
					_, responseErr := resp.Write(c.BodyBuffer.Bytes())
					if responseErr != nil {
						d.EmitError(c, responseErr)
					}
				} else if c.IsReaderBody() {
					r, _ := c.Body.(io.Reader)
					_, pipeErr := c.Pipe(r)
					if pipeErr != nil {
						d.EmitError(c, pipeErr)
					}
				}
			}
		}
		c.Committed = true
		if !c.reuseDisabled {
			d.ctxPool.Put(c)
		}
	})
}

// AddValidator add validate function
func (d *Elton) AddValidator(key string, fn Validator) {
	if d.validators == nil {
		d.validators = make(map[string]Validator)
	}
	d.validators[key] = fn
}

// GET add http get method handle
func (d *Elton) GET(path string, handlerList ...Handler) {
	d.Handle(http.MethodGet, path, handlerList...)
}

// POST add http post method handle
func (d *Elton) POST(path string, handlerList ...Handler) {
	d.Handle(http.MethodPost, path, handlerList...)
}

// PUT add http put method handle
func (d *Elton) PUT(path string, handlerList ...Handler) {
	d.Handle(http.MethodPut, path, handlerList...)
}

// PATCH add http patch method handle
func (d *Elton) PATCH(path string, handlerList ...Handler) {
	d.Handle(http.MethodPatch, path, handlerList...)
}

// DELETE add http delete method handle
func (d *Elton) DELETE(path string, handlerList ...Handler) {
	d.Handle(http.MethodDelete, path, handlerList...)
}

// HEAD add http head method handle
func (d *Elton) HEAD(path string, handlerList ...Handler) {
	d.Handle(http.MethodHead, path, handlerList...)
}

// OPTIONS add http options method handle
func (d *Elton) OPTIONS(path string, handlerList ...Handler) {
	d.Handle(http.MethodOptions, path, handlerList...)
}

// TRACE add http trace method handle
func (d *Elton) TRACE(path string, handlerList ...Handler) {
	d.Handle(http.MethodTrace, path, handlerList...)
}

// ALL add http all method handle
func (d *Elton) ALL(path string, handlerList ...Handler) {
	for _, method := range methods {
		d.Handle(method, path, handlerList...)
	}
}

// Use add middleware function handle
func (d *Elton) Use(handlerList ...Handler) {
	if d.Middlewares == nil {
		d.Middlewares = make([]Handler, 0)
	}
	for _, fn := range handlerList {
		name := d.GetFunctionName(fn)
		d.SetFunctionName(fn, name)
	}
	d.Middlewares = append(d.Middlewares, handlerList...)
}

// Pre add pre middleware function handler
func (d *Elton) Pre(handlerList ...PreHandler) {
	if d.PreMiddlewares == nil {
		d.PreMiddlewares = make([]PreHandler, 0)
	}
	d.PreMiddlewares = append(d.PreMiddlewares, handlerList...)
}

// NotFound not found handle
func (d *Elton) NotFound(resp http.ResponseWriter, req *http.Request) {
	if d.NotFoundHandler != nil {
		d.NotFoundHandler(resp, req)
		return
	}
	resp.WriteHeader(http.StatusNotFound)
	resp.Write([]byte("Not found"))
}

// Error error handle
func (d *Elton) Error(c *Context, err error) {
	// 出错时清除部分响应头
	for _, key := range []string{
		HeaderETag,
		HeaderLastModified,
		HeaderContentEncoding,
		HeaderContentLength,
	} {
		c.SetHeader(key, "")
	}
	if d.ErrorHandler != nil {
		d.ErrorHandler(c, err)
		return
	}

	resp := c.Response
	he, ok := err.(*hes.Error)
	if ok {
		resp.WriteHeader(he.StatusCode)
		resp.Write([]byte(he.Error()))
	} else {
		resp.WriteHeader(http.StatusInternalServerError)
		resp.Write([]byte(err.Error()))
	}
}

// EmitError emit error function
func (d *Elton) EmitError(c *Context, err error) {
	lns := d.errorListeners
	for _, ln := range lns {
		ln(c, err)
	}
}

// OnError on error function
func (d *Elton) OnError(ln ErrorListener) {
	if d.errorListeners == nil {
		d.errorListeners = make([]ErrorListener, 0)
	}
	d.errorListeners = append(d.errorListeners, ln)
}

// EmitTrace emit trace
func (d *Elton) EmitTrace(c *Context, infos TraceInfos) {
	lns := d.traceListeners
	for _, ln := range lns {
		ln(c, infos)
	}
}

// OnTrace on trace function
func (d *Elton) OnTrace(ln TraceListener) {
	if d.traceListeners == nil {
		d.traceListeners = make([]TraceListener, 0)
	}
	d.traceListeners = append(d.traceListeners, ln)
}

// AddGroup add the group to elton
func (d *Elton) AddGroup(g *Group) {
	for _, r := range g.routers {
		d.Handle(r.Method, r.Path, r.HandleList...)
	}
}

func (g *Group) merge(s2 []Handler) []Handler {
	s1 := g.HandlerList
	fns := make([]Handler, len(s1)+len(s2))
	copy(fns, s1)
	copy(fns[len(s1):], s2)
	return fns
}

func (g *Group) add(method, path string, handlerList ...Handler) {
	if g.routers == nil {
		g.routers = make([]*Router, 0, 5)
	}
	g.routers = append(g.routers, &Router{
		Method:     method,
		Path:       path,
		HandleList: handlerList,
	})
}

// GET add group http get method handler
func (g *Group) GET(path string, handlerList ...Handler) {
	p := g.Path + path
	fns := g.merge(handlerList)
	g.add(http.MethodGet, p, fns...)
}

// POST add group http post method handler
func (g *Group) POST(path string, handlerList ...Handler) {
	p := g.Path + path
	fns := g.merge(handlerList)
	g.add(http.MethodPost, p, fns...)
}

// PUT add group http put method handler
func (g *Group) PUT(path string, handlerList ...Handler) {
	p := g.Path + path
	fns := g.merge(handlerList)
	g.add(http.MethodPut, p, fns...)
}

// PATCH add group http patch method handler
func (g *Group) PATCH(path string, handlerList ...Handler) {
	p := g.Path + path
	fns := g.merge(handlerList)
	g.add(http.MethodPatch, p, fns...)
}

// DELETE add group http delete method handler
func (g *Group) DELETE(path string, handlerList ...Handler) {
	p := g.Path + path
	fns := g.merge(handlerList)
	g.add(http.MethodDelete, p, fns...)
}

// HEAD add group http head method handler
func (g *Group) HEAD(path string, handlerList ...Handler) {
	p := g.Path + path
	fns := g.merge(handlerList)
	g.add(http.MethodHead, p, fns...)
}

// OPTIONS add group http options method handler
func (g *Group) OPTIONS(path string, handlerList ...Handler) {
	p := g.Path + path
	fns := g.merge(handlerList)
	g.add(http.MethodOptions, p, fns...)
}

// TRACE add group http trace method handler
func (g *Group) TRACE(path string, handlerList ...Handler) {
	p := g.Path + path
	fns := g.merge(handlerList)
	g.add(http.MethodTrace, p, fns...)
}

// ALL add group http all method handler
func (g *Group) ALL(path string, handlerList ...Handler) {
	p := g.Path + path
	fns := g.merge(handlerList)
	for _, method := range methods {
		g.add(method, p, fns...)
	}
}

// Compose compose handler list
func Compose(handlerList ...Handler) Handler {
	max := len(handlerList)
	if max == 0 {
		panic(errors.New("handler function is required"))
	}
	return func(c *Context) (err error) {
		// 保存原有的next函数
		originalNext := c.Next
		index := -1
		// 新创建一个next的调用链
		c.Next = func() error {
			index++
			// 如果已执行成所有的next，则转回原有的调用链
			if index >= max {
				c.Next = originalNext
				return c.Next()
			}
			return handlerList[index](c)
		}
		return c.Next()
	}
}

func getMs(ns int) string {
	microSecond := int(time.Microsecond)
	milliSecond := int(time.Millisecond)
	if ns < microSecond {
		return "0"
	}

	// 计算ms的位
	ms := ns / milliSecond
	prefix := strconv.Itoa(ms)

	// 计算micro seconds
	offset := (ns % milliSecond) / microSecond
	// 如果小于10，不展示小数点（取小数点两位）
	unit := 10
	if offset < unit {
		return prefix
	}
	// 如果小于100，补一位0
	if offset < 100 {
		return prefix + ".0" + strconv.Itoa(offset/unit)
	}
	return prefix + "." + strconv.Itoa(offset/unit)
}

// ServerTiming trace infos to server timing
func (traceInfos TraceInfos) ServerTiming(prefix string) string {
	size := len(traceInfos)
	if size == 0 {
		return ""
	}

	// 转换为 http server timing
	s := new(strings.Builder)
	// 每一个server timing长度预估为30
	s.Grow(30 * size)
	for i, traceInfo := range traceInfos {
		v := traceInfo.Duration.Nanoseconds()
		s.WriteString(prefix)
		s.WriteString(strconv.Itoa(i))
		s.Write(ServerTimingDur)
		s.WriteString(getMs(int(v)))
		s.Write(ServerTimingDesc)
		s.WriteString(traceInfo.Name)
		s.Write(ServerTimingEnd)
		if i != size-1 {
			s.WriteRune(',')
		}

	}
	return s.String()
}