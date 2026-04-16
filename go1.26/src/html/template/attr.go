// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"strings"
)

// attrTypeMap[n] 描述给定属性的值类型。
// 如果某个属性会影响（或可能掩盖）其他内容的编码或解释方式，
// 或者影响网络消息的内容、幂等性或凭据，
// 则该映射中对应的值为 contentTypeUnsafe。
// 此映射派生自 HTML5，具体来自
// https://www.w3.org/TR/html5/Overview.html#attributes-1
// 以及来自以下文档的 "%URI" 类型属性
// https://www.w3.org/TR/html4/index/attributes.html
var attrTypeMap = map[string]contentType{
	"accept":          contentTypePlain,
	"accept-charset":  contentTypeUnsafe,
	"action":          contentTypeURL,
	"alt":             contentTypePlain,
	"archive":         contentTypeURL,
	"async":           contentTypeUnsafe,
	"autocomplete":    contentTypePlain,
	"autofocus":       contentTypePlain,
	"autoplay":        contentTypePlain,
	"background":      contentTypeURL,
	"border":          contentTypePlain,
	"checked":         contentTypePlain,
	"cite":            contentTypeURL,
	"challenge":       contentTypeUnsafe,
	"charset":         contentTypeUnsafe,
	"class":           contentTypePlain,
	"classid":         contentTypeURL,
	"codebase":        contentTypeURL,
	"cols":            contentTypePlain,
	"colspan":         contentTypePlain,
	"content":         contentTypeUnsafe,
	"contenteditable": contentTypePlain,
	"contextmenu":     contentTypePlain,
	"controls":        contentTypePlain,
	"coords":          contentTypePlain,
	"crossorigin":     contentTypeUnsafe,
	"data":            contentTypeURL,
	"datetime":        contentTypePlain,
	"default":         contentTypePlain,
	"defer":           contentTypeUnsafe,
	"dir":             contentTypePlain,
	"dirname":         contentTypePlain,
	"disabled":        contentTypePlain,
	"draggable":       contentTypePlain,
	"dropzone":        contentTypePlain,
	"enctype":         contentTypeUnsafe,
	"for":             contentTypePlain,
	"form":            contentTypeUnsafe,
	"formaction":      contentTypeURL,
	"formenctype":     contentTypeUnsafe,
	"formmethod":      contentTypeUnsafe,
	"formnovalidate":  contentTypeUnsafe,
	"formtarget":      contentTypePlain,
	"headers":         contentTypePlain,
	"height":          contentTypePlain,
	"hidden":          contentTypePlain,
	"high":            contentTypePlain,
	"href":            contentTypeURL,
	"hreflang":        contentTypePlain,
	"http-equiv":      contentTypeUnsafe,
	"icon":            contentTypeURL,
	"id":              contentTypePlain,
	"ismap":           contentTypePlain,
	"keytype":         contentTypeUnsafe,
	"kind":            contentTypePlain,
	"label":           contentTypePlain,
	"lang":            contentTypePlain,
	"language":        contentTypeUnsafe,
	"list":            contentTypePlain,
	"longdesc":        contentTypeURL,
	"loop":            contentTypePlain,
	"low":             contentTypePlain,
	"manifest":        contentTypeURL,
	"max":             contentTypePlain,
	"maxlength":       contentTypePlain,
	"media":           contentTypePlain,
	"mediagroup":      contentTypePlain,
	"method":          contentTypeUnsafe,
	"min":             contentTypePlain,
	"multiple":        contentTypePlain,
	"name":            contentTypePlain,
	"novalidate":      contentTypeUnsafe,
	// 跳过以下文档中的事件处理器名称
	// https://www.w3.org/TR/html5/webappapis.html#event-handlers-on-elements,-document-objects,-and-window-objects
	// 因为我们在 attrType 中有专门的处理逻辑。
	"open":        contentTypePlain,
	"optimum":     contentTypePlain,
	"pattern":     contentTypeUnsafe,
	"placeholder": contentTypePlain,
	"poster":      contentTypeURL,
	"profile":     contentTypeURL,
	"preload":     contentTypePlain,
	"pubdate":     contentTypePlain,
	"radiogroup":  contentTypePlain,
	"readonly":    contentTypePlain,
	"rel":         contentTypeUnsafe,
	"required":    contentTypePlain,
	"reversed":    contentTypePlain,
	"rows":        contentTypePlain,
	"rowspan":     contentTypePlain,
	"sandbox":     contentTypeUnsafe,
	"spellcheck":  contentTypePlain,
	"scope":       contentTypePlain,
	"scoped":      contentTypePlain,
	"seamless":    contentTypePlain,
	"selected":    contentTypePlain,
	"shape":       contentTypePlain,
	"size":        contentTypePlain,
	"sizes":       contentTypePlain,
	"span":        contentTypePlain,
	"src":         contentTypeURL,
	"srcdoc":      contentTypeHTML,
	"srclang":     contentTypePlain,
	"srcset":      contentTypeSrcset,
	"start":       contentTypePlain,
	"step":        contentTypePlain,
	"style":       contentTypeCSS,
	"tabindex":    contentTypePlain,
	"target":      contentTypePlain,
	"title":       contentTypePlain,
	"type":        contentTypeUnsafe,
	"usemap":      contentTypeURL,
	"value":       contentTypeUnsafe,
	"width":       contentTypePlain,
	"wrap":        contentTypePlain,
	"xmlns":       contentTypeURL,
}

// attrType 返回对小写命名属性类型的保守（权限上界）猜测。
func attrType(name string) contentType {
	if strings.HasPrefix(name, "data-") {
		// 去除 data- 前缀，以便下方的自定义属性启发式规则能被广泛应用。
		// 将 data-action 视为下方的 URL 类型。
		name = name[5:]
	} else if prefix, short, ok := strings.Cut(name, ":"); ok {
		if prefix == "xmlns" {
			return contentTypeURL
		}
		// 将 svg:href 和 xlink:href 视为下方的 href。
		name = short
	}
	if t, ok := attrTypeMap[name]; ok {
		return t
	}
	// 将部分事件处理器名称视为脚本。
	if strings.HasPrefix(name, "on") {
		return contentTypeJS
	}

	// 用于防止 "javascript:..." 注入到自定义 data 属性和
	// 自定义属性（如 g:tweetUrl）中的启发式规则。
	// https://www.w3.org/TR/html5/dom.html#embedding-custom-non-visible-data-with-the-data-*-attributes
	// "自定义 data 属性旨在存储页面或应用程序私有的自定义数据，
	//  对于这些数据没有更合适的属性或元素可用。"
	// 开发者似乎会将 URL 内容存储在以 "URI" 或 "URL" 开头或结尾的 data URL 中。
	if strings.Contains(name, "src") ||
		strings.Contains(name, "uri") ||
		strings.Contains(name, "url") {
		return contentTypeURL
	}
	return contentTypePlain
}
