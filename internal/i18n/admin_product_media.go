package i18n

var (
	KeyAdminProdImages = key("admin.prod.images", Message{ZhHant: "商品圖片", En: "Product images"})

	KeyAdminProdUpload = key("admin.prod.upload", Message{ZhHant: "上傳圖片", En: "Upload an image"})

	KeyAdminProdUploadHint = key("admin.prod.uploadhint", Message{
		ZhHant: "JPEG、PNG、GIF 或 WebP,8 MB 以內。上傳後會由伺服器重新編碼。",
		En:     "JPEG, PNG, GIF or WebP, 8 MB at most. The server re-encodes whatever it accepts.",
	})

	KeyAdminProdAltHint = key("admin.prod.althint", Message{
		ZhHant: "讀螢幕的人靠這句話知道圖裡是什麼,所以是必填。",
		En: "Somebody using a screen reader learns what the picture shows from this sentence, " +
			"which is why it is required.",
	})

	KeyAdminProdAltEn = key("admin.prod.alten", Message{
		ZhHant: "替代文字(英文)",
		En:     "Alt text (English)",
	})

	KeyAdminProdAltEnHint = key("admin.prod.altenhint", Message{
		ZhHant: "螢幕閱讀器會用頁面語言唸這段話,英文頁面唸中文會唸不出來。留空就沿用中文。",
		En: "A screen reader announces this in the page's own language, so Chinese alt text on an " +
			"English page is announced in the wrong voice or not at all. Leave it blank to fall " +
			"back to the Chinese.",
	})

	KeyAdminProdLibrary = key("admin.prod.library", Message{
		ZhHant: "或選一張已經上傳過的",
		En:     "Or pick one already uploaded",
	})

	KeyAdminProdReuse = key("admin.prod.reuse", Message{ZhHant: "使用", En: "Use"})
)
