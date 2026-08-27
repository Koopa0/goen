package i18n

var (
	KeyAdminShipLead = key("admin.ship.lead", Message{
		ZhHant: "改運費是「發布新版本」,不是改舊的 —— 每一張過去的訂單都記著自己是用哪個版本計價的。",
		En: "Changing a fee publishes a NEW version rather than editing the old one — every past " +
			"order records which version it was priced from.",
	})

	KeyAdminShipCurrent = key("admin.ship.current", Message{
		ZhHant: "目前:%s,滿 %s 免運。",
		En:     "Currently %s, free delivery over %s.",
	})
	KeyAdminShipSince = key("admin.ship.since", Message{
		ZhHant: "%s 起生效,共 %s 個版本。",
		En:     "In force since %s, %s versions in all.",
	})

	KeyAdminShipCarrierOptional = key("admin.ship.carrier.optional", Message{
		ZhHant: "物流商(選填)",
		En:     "Carrier (optional)",
	})
	KeyAdminShipCarrierEn = key("admin.ship.carrier.en", Message{
		ZhHant: "物流商(英文)",
		En:     "Carrier (English)",
	})
	KeyAdminShipFee = key("admin.ship.fee", Message{
		ZhHant: "運費(元)",
		En:     "Delivery fee (NT$)",
	})
	KeyAdminShipFreeOver = key("admin.ship.freeover", Message{
		ZhHant: "免運門檻(元,留空為無)",
		En:     "Free-delivery threshold (NT$, blank for none)",
	})
	KeyAdminShipPublish = key("admin.ship.publish", Message{
		ZhHant: "發布新版本",
		En:     "Publish a new version",
	})

	KeyAdminShipDisable = key("admin.ship.disable", Message{
		ZhHant: "停用這個方式",
		En:     "Switch this method off",
	})
	KeyAdminShipReenable = key("admin.ship.reenable", Message{
		ZhHant: "重新啟用",
		En:     "Switch it back on",
	})
)

var (
	KeyAdminShipSurcharges = key("admin.ship.surcharges", Message{
		ZhHant: "分區加價",
		En:     "Zone surcharges",
	})
	KeyAdminShipSurchargeLead = key("admin.ship.surcharge.lead", Message{
		ZhHant: "加在免運之後 —— 免運是本店對自己基本運費的優惠,跨海的錢是物流商收的。填 0 就是取消加價。",
		En: "Added AFTER the free-delivery threshold — free delivery is this shop's own offer on its " +
			"own base rate, and the carrier still charges to cross the water. Enter 0 to clear a surcharge.",
	})
	KeyAdminShipPrefixCount = key("admin.ship.prefixcount", Message{
		ZhHant: "%s 個郵遞區號",
		En:     "%s postal codes",
	})
	KeyAdminShipSet    = key("admin.ship.set", Message{ZhHant: "設定", En: "Set"})
	KeyAdminShipNoZone = key("admin.ship.nozone", Message{
		ZhHant: "這個方式收件到門市,沒有郵遞區號,所以永遠不會落在任何分區裡。",
		En: "This method delivers to a store, so it has no postal code and can never fall inside " +
			"any zone.",
	})
)

var (
	KeyAdminShipAddMethod = key("admin.ship.addmethod", Message{
		ZhHant: "新增配送方式",
		En:     "Add a delivery method",
	})
	KeyAdminShipCodeHint = key("admin.ship.code.hint", Message{
		ZhHant: "結帳的網址會帶這個代碼,之後不能改。",
		En:     "The checkout URL carries this code, and it cannot be changed afterwards.",
	})
	KeyAdminShipDestination = key("admin.ship.destination", Message{
		ZhHant: "送到哪裡",
		En:     "Where it delivers",
	})
	KeyAdminShipToAddress = key("admin.ship.toaddress", Message{
		ZhHant: "收件地址",
		En:     "Delivery address",
	})
	KeyAdminShipDestinationHint = key("admin.ship.destination.hint", Message{
		ZhHant: "這一項決定結帳要問街道還是問門市,選錯了收不到貨。",
		En: "This decides whether the checkout asks for a street or for a store. Choose the wrong " +
			"one and the parcel cannot be delivered.",
	})

	KeyAdminShipMaxLongest = key("admin.ship.maxlongest", Message{
		ZhHant: "包裹上限:最長邊(mm)",
		En:     "Parcel limit: longest side (mm)",
	})
	KeyAdminShipMaxSum = key("admin.ship.maxsum", Message{
		ZhHant: "包裹上限:三邊合(mm)",
		En:     "Parcel limit: three sides added (mm)",
	})
	KeyAdminShipMaxWeight = key("admin.ship.maxweight", Message{
		ZhHant: "包裹上限:重量(g)",
		En:     "Parcel limit: weight (g)",
	})
	KeyAdminShipLimitsHint = key("admin.ship.limits.hint", Message{
		ZhHant: "空白代表沒有上限。超商店到店是 450 / 1050 / 10000,萊爾富重量只到 5000。",
		En: "Blank means no limit. Convenience-store counter-to-counter is 450 / 1050 / 10000, " +
			"and Hi-Life takes only 5000 by weight.",
	})

	KeyAdminShipNamePlaceholder = key("admin.ship.name.placeholder", Message{
		ZhHant: "隔日到貨",
		En:     "Next-day delivery",
	})
	KeyAdminShipFirstVersion = key("admin.ship.firstversion", Message{
		ZhHant: "新增方式時會一起發布第一個版本 —— 沒有版本的方式結帳找得到卻算不出運費。",
		En: "Adding a method publishes its first version with it — a method with no version is one " +
			"the checkout finds and cannot price.",
	})
)

var (
	KeyAdminShipZones    = key("admin.ship.zones", Message{ZhHant: "分區", En: "Zones"})
	KeyAdminShipColZone  = key("admin.ship.col.zone", Message{ZhHant: "分區", En: "Zone"})
	KeyAdminShipPrefixes = key("admin.ship.prefixes", Message{
		ZhHant: "郵遞區號前三碼",
		En:     "First three digits of the postal code",
	})
	KeyAdminShipZonePrefixes = key("admin.ship.zone.prefixes", Message{
		ZhHant: "%s 的郵遞區號",
		En:     "Postal codes for %s",
	})

	KeyAdminShipAddZone             = key("admin.ship.addzone", Message{ZhHant: "新增分區", En: "Add a zone"})
	KeyAdminShipZoneNamePlaceholder = key("admin.ship.zone.name.placeholder", Message{
		ZhHant: "離島",
		En:     "Outlying islands",
	})
	KeyAdminShipZoneNameEnHint = key("admin.ship.zone.nameen.hint", Message{
		ZhHant: "/shipping 和結帳的加價說明都會讀這個名字。",
		En:     "/shipping and the surcharge sentence at checkout both read this name.",
	})
	KeyAdminShipPrefixesHint = key("admin.ship.prefixes.hint", Message{
		ZhHant: "空白或逗號分隔。新增分區時至少填一個;之後可把上方欄位清空,讓分區不再命中任何郵遞區號。",
		En: "Separated by spaces or commas. A new zone needs at least one; clear its field later " +
			"to make it match no postal code.",
	})
)
