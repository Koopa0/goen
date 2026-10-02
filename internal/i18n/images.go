package i18n

var (
	KeyAdminProdImages = key("admin.prod.images", Message{ZhHant: "商品圖片", En: "Product images"})

	KeyAdminProdUpload = key("admin.prod.upload", Message{ZhHant: "上傳圖片", En: "Upload an image"})

	KeyAdminProdUploadHint = key("admin.prod.uploadhint", Message{
		ZhHant: "JPEG、PNG、GIF 或 WebP，8 MB 以內。頁面會從中央裁成正方形顯示，主體請放在中間；長邊超過 2400 像素會被縮小。上傳後會由伺服器重新編碼。",
		En: "JPEG, PNG, GIF or WebP, 8 MB at most. Pages show it cropped to a square from the centre, " +
			"so keep the subject in the middle; a long side beyond 2400 pixels is scaled down. " +
			"The server re-encodes whatever it accepts.",
	})

	KeyAdminProdAltHint = key("admin.prod.althint", Message{
		ZhHant: "讀螢幕的人靠這句話知道圖裡是什麼，所以是必填。",
		En: "Somebody using a screen reader learns what the picture shows from this sentence, " +
			"which is why it is required.",
	})

	KeyAdminProdAltEn = key("admin.prod.alten", Message{
		ZhHant: "替代文字（英文）",
		En:     "Alt text (English)",
	})

	KeyAdminProdAltEnHint = key("admin.prod.altenhint", Message{
		ZhHant: "螢幕閱讀器會用頁面語言唸這段話，英文頁面唸中文會唸不出來。留空就沿用中文。",
		En: "A screen reader announces this in the page's own language, so Chinese alt text on an " +
			"English page is announced in the wrong voice or not at all. Leave it blank to fall " +
			"back to the Chinese.",
	})

	KeyAdminProdLibrary = key("admin.prod.library", Message{
		ZhHant: "或選一張已經上傳過的",
		En:     "Or pick one already uploaded",
	})

	KeyAdminImageCover = key("admin.prod.imagecover", Message{ZhHant: "設為封面", En: "Set as cover"})
	KeyAdminImageUp    = key("admin.prod.imageup", Message{ZhHant: "往前移", En: "Move earlier"})
	KeyAdminImageDown  = key("admin.prod.imagedown", Message{ZhHant: "往後移", En: "Move later"})

	KeyAdminNoticeImageStale = key("admin.notice.imagestale", Message{
		ZhHant: "圖片順序已經有變動，這個動作沒有執行。請看一下目前的順序再試一次。",
		En:     "The image order changed, so that move was not made. Check the current order and try again.",
	})

	KeyAdminProdReuse = key("admin.prod.reuse", Message{ZhHant: "使用", En: "Use"})

	KeyAdminProdImageOption = key("admin.prod.imageoption", Message{
		ZhHant: "這張照片是哪個選項",
		En:     "Which option this photo shows",
	})

	KeyAdminProdImageAnyOption = key("admin.prod.imageanyoption", Message{ZhHant: "不限選項", En: "Any option"})

	KeyAdminProdImageOptionHint = key("admin.prod.imageoptionhint", Message{
		ZhHant: "顧客選了這個選項時，這張照片排在最前面。",
		En:     "When a customer chooses this option, this photo comes first.",
	})
)

var (
	KeyAdminNoticeTooBig = key("admin.notice.toobig", Message{
		ZhHant: "圖片太大了，請用 8 MB 以內、像素尺寸較小的檔案。",
		En:     "That image is too large. Use a file under 8 MB with smaller pixel dimensions.",
	})

	KeyAdminNoticeLosslessWebP = key("admin.notice.losslesswebp", Message{
		ZhHant: "不接受無損 WebP 圖片，請改上傳 PNG 或有損 WebP。",
		En:     "Lossless WebP images are not accepted. Upload a PNG or a lossy WebP instead.",
	})

	KeyAdminNoticeUploadBusy = key("admin.notice.uploadbusy", Message{
		ZhHant: "其他圖片正在處理中，請稍候再上傳一次。",
		En:     "Other images are being processed. Please upload again in a moment.",
	})

	KeyAdminNoticeNotImage = key("admin.notice.notimage", Message{
		ZhHant: "這個檔案不是可以辨識的圖片。支援 JPEG、PNG、GIF 與 WebP。",
		En:     "That file is not an image goen can decode. JPEG, PNG, GIF and WebP are supported.",
	})

	KeyAdminNoticeUploadFailed = key("admin.notice.uploadfailed", Message{
		ZhHant: "圖片上傳失敗，請再試一次。",
		En:     "The upload did not finish. Please try again.",
	})

	KeyAdminNoticeAttachRefused = key("admin.notice.attachrefused", Message{
		ZhHant: "這張圖片已經在這個商品上了。",
		En:     "That image is already on this product.",
	})

	KeyAdminNoticeBadOption = key("admin.notice.badoption", Message{
		ZhHant: "那個選項不是這個商品的。",
		En:     "That option is not one of this product's.",
	})

	KeyAdminNoticeNoAlt = key("admin.notice.noalt", Message{
		ZhHant: "請填寫圖片說明文字 —— 讀螢幕的人靠它知道圖裡是什麼。",
		En:     "Alt text is required — it is how somebody using a screen reader knows what the picture shows.",
	})
)
