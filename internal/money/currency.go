package money

import "strings"

// Currency 是一个受支持的法币。
type Currency struct {
	Code   string
	Name   string
	Symbol string
}

// currencies 是受支持的币种,顺序即下拉列表中的展示顺序。
var currencies = []Currency{
	{Code: "CNY", Name: "人民币", Symbol: "\uFFE5"},
	{Code: "USD", Name: "美元", Symbol: "$"},
	{Code: "EUR", Name: "欧元", Symbol: "\u20AC"},
	{Code: "GBP", Name: "英镑", Symbol: "\u00A3"},
	{Code: "JPY", Name: "日元", Symbol: "\u00A5"},
	{Code: "HKD", Name: "港币", Symbol: "HK$"},
	{Code: "TWD", Name: "新台币", Symbol: "NT$"},
	{Code: "MOP", Name: "澳门元", Symbol: "MOP"},
	{Code: "SGD", Name: "新加坡元", Symbol: "S$"},
	{Code: "AUD", Name: "澳元", Symbol: "A$"},
	{Code: "NZD", Name: "新西兰元", Symbol: "NZ$"},
	{Code: "CAD", Name: "加元", Symbol: "C$"},
	{Code: "CHF", Name: "瑞士法郎", Symbol: "CHF"},
	{Code: "RUB", Name: "俄罗斯卢布", Symbol: "\u20BD"},
	{Code: "KRW", Name: "韩元", Symbol: "\u20A9"},
	{Code: "THB", Name: "泰铢", Symbol: "\u0E3F"},
	{Code: "VND", Name: "越南盾", Symbol: "\u20AB"},
	{Code: "PHP", Name: "菲律宾比索", Symbol: "\u20B1"},
	{Code: "MYR", Name: "马来西亚林吉特", Symbol: "RM"},
	{Code: "IDR", Name: "印尼盾", Symbol: "Rp"},
	{Code: "INR", Name: "印度卢比", Symbol: "\u20B9"},
	{Code: "PKR", Name: "巴基斯坦卢比", Symbol: "PKR"},
	{Code: "BDT", Name: "孟加拉塔卡", Symbol: "BDT"},
	{Code: "LKR", Name: "斯里兰卡卢比", Symbol: "LKR"},
	{Code: "NPR", Name: "尼泊尔卢比", Symbol: "NPR"},
	{Code: "BTN", Name: "不丹努尔特鲁姆", Symbol: "BTN"},
	{Code: "MVR", Name: "马尔代夫拉菲亚", Symbol: "MVR"},
	{Code: "BND", Name: "文莱元", Symbol: "B$"},
	{Code: "MMK", Name: "缅甸缅元", Symbol: "MMK"},
	{Code: "KHR", Name: "柬埔寨瑞尔", Symbol: "KHR"},
	{Code: "LAK", Name: "老挝基普", Symbol: "LAK"},
	{Code: "MNT", Name: "蒙古图格里克", Symbol: "\u20AE"},
	{Code: "BRL", Name: "巴西雷亚尔", Symbol: "R$"},
	{Code: "MXN", Name: "墨西哥比索", Symbol: "Mex$"},
	{Code: "ARS", Name: "阿根廷比索", Symbol: "ARS"},
	{Code: "CLP", Name: "智利比索", Symbol: "CLP"},
	{Code: "COP", Name: "哥伦比亚比索", Symbol: "COP"},
	{Code: "PEN", Name: "秘鲁索尔", Symbol: "PEN"},
	{Code: "UYU", Name: "乌拉圭比索", Symbol: "UYU"},
	{Code: "PYG", Name: "巴拉圭瓜拉尼", Symbol: "PYG"},
	{Code: "BOB", Name: "玻利维亚诺", Symbol: "BOB"},
	{Code: "CRC", Name: "哥斯达黎加科朗", Symbol: "CRC"},
	{Code: "GTQ", Name: "危地马拉格查尔", Symbol: "GTQ"},
	{Code: "HNL", Name: "洪都拉斯伦皮拉", Symbol: "HNL"},
	{Code: "NIO", Name: "尼加拉瓜科多巴", Symbol: "NIO"},
	{Code: "DOP", Name: "多米尼加比索", Symbol: "DOP"},
	{Code: "JMD", Name: "牙买加元", Symbol: "J$"},
	{Code: "TTD", Name: "特立尼达和多巴哥元", Symbol: "TT$"},
	{Code: "BSD", Name: "巴哈马元", Symbol: "B$"},
	{Code: "BBD", Name: "巴巴多斯元", Symbol: "Bds$"},
	{Code: "BZD", Name: "伯利兹元", Symbol: "BZ$"},
	{Code: "GYD", Name: "圭亚那元", Symbol: "GYD"},
	{Code: "SRD", Name: "苏里南元", Symbol: "SRD"},
	{Code: "XCD", Name: "东加勒比元", Symbol: "EC$"},
	{Code: "PLN", Name: "波兰兹罗提", Symbol: "z\u0142"},
	{Code: "CZK", Name: "捷克克朗", Symbol: "K\u010D"},
	{Code: "HUF", Name: "匈牙利福林", Symbol: "Ft"},
	{Code: "RON", Name: "罗马尼亚列伊", Symbol: "lei"},
	{Code: "BGN", Name: "保加利亚列弗", Symbol: "BGN"},
	{Code: "SEK", Name: "瑞典克朗", Symbol: "kr"},
	{Code: "NOK", Name: "挪威克朗", Symbol: "kr"},
	{Code: "DKK", Name: "丹麦克朗", Symbol: "kr"},
	{Code: "ISK", Name: "冰岛克朗", Symbol: "kr"},
	{Code: "RSD", Name: "塞尔维亚第纳尔", Symbol: "RSD"},
	{Code: "MKD", Name: "北马其顿代纳尔", Symbol: "MKD"},
	{Code: "ALL", Name: "阿尔巴尼亚列克", Symbol: "ALL"},
	{Code: "BAM", Name: "波黑可兑换马克", Symbol: "BAM"},
	{Code: "MDL", Name: "摩尔多瓦列伊", Symbol: "MDL"},
	{Code: "UAH", Name: "乌克兰格里夫纳", Symbol: "\u20B4"},
	{Code: "BYN", Name: "白俄罗斯卢布", Symbol: "BYN"},
	{Code: "GEL", Name: "格鲁吉亚拉里", Symbol: "\u20BE"},
	{Code: "AMD", Name: "亚美尼亚德拉姆", Symbol: "AMD"},
	{Code: "AZN", Name: "阿塞拜疆马纳特", Symbol: "\u20BC"},
	{Code: "KZT", Name: "哈萨克斯坦坚戈", Symbol: "\u20B8"},
	{Code: "UZS", Name: "乌兹别克斯坦苏姆", Symbol: "UZS"},
	{Code: "KGS", Name: "吉尔吉斯斯坦索姆", Symbol: "KGS"},
	{Code: "TJS", Name: "塔吉克斯坦索莫尼", Symbol: "TJS"},
	{Code: "TMT", Name: "土库曼斯坦马纳特", Symbol: "TMT"},
	{Code: "TRY", Name: "土耳其里拉", Symbol: "\u20BA"},
	{Code: "ILS", Name: "以色列新谢克尔", Symbol: "\u20AA"},
	{Code: "AED", Name: "阿联酋迪拉姆", Symbol: "AED"},
	{Code: "SAR", Name: "沙特里亚尔", Symbol: "SAR"},
	{Code: "QAR", Name: "卡塔尔里亚尔", Symbol: "QAR"},
	{Code: "KWD", Name: "科威特第纳尔", Symbol: "KWD"},
	{Code: "BHD", Name: "巴林第纳尔", Symbol: "BHD"},
	{Code: "OMR", Name: "阿曼里亚尔", Symbol: "OMR"},
	{Code: "JOD", Name: "约旦第纳尔", Symbol: "JOD"},
	{Code: "LBP", Name: "黎巴嫩镑", Symbol: "LBP"},
	{Code: "IQD", Name: "伊拉克第纳尔", Symbol: "IQD"},
	{Code: "IRR", Name: "伊朗里亚尔", Symbol: "IRR"},
	{Code: "YER", Name: "也门里亚尔", Symbol: "YER"},
	{Code: "AFN", Name: "阿富汗尼", Symbol: "AFN"},
	{Code: "EGP", Name: "埃及镑", Symbol: "E\u00A3"},
	{Code: "MAD", Name: "摩洛哥迪拉姆", Symbol: "MAD"},
	{Code: "TND", Name: "突尼斯第纳尔", Symbol: "TND"},
	{Code: "DZD", Name: "阿尔及利亚第纳尔", Symbol: "DZD"},
	{Code: "ZAR", Name: "南非兰特", Symbol: "R"},
	{Code: "NGN", Name: "尼日利亚奈拉", Symbol: "\u20A6"},
	{Code: "KES", Name: "肯尼亚先令", Symbol: "KSh"},
	{Code: "GHS", Name: "加纳塞地", Symbol: "GH\u20B5"},
	{Code: "ETB", Name: "埃塞俄比亚比尔", Symbol: "ETB"},
	{Code: "UGX", Name: "乌干达先令", Symbol: "USh"},
	{Code: "TZS", Name: "坦桑尼亚先令", Symbol: "TSh"},
	{Code: "RWF", Name: "卢旺达法郎", Symbol: "RWF"},
	{Code: "BIF", Name: "布隆迪法郎", Symbol: "BIF"},
	{Code: "MWK", Name: "马拉维克瓦查", Symbol: "MWK"},
	{Code: "ZMW", Name: "赞比亚克瓦查", Symbol: "ZMW"},
	{Code: "MZN", Name: "莫桑比克梅蒂卡尔", Symbol: "MZN"},
	{Code: "AOA", Name: "安哥拉宽扎", Symbol: "AOA"},
	{Code: "BWP", Name: "博茨瓦纳普拉", Symbol: "BWP"},
	{Code: "NAD", Name: "纳米比亚元", Symbol: "N$"},
	{Code: "LSL", Name: "莱索托洛蒂", Symbol: "LSL"},
	{Code: "SZL", Name: "斯威士兰里兰吉尼", Symbol: "SZL"},
	{Code: "MUR", Name: "毛里求斯卢比", Symbol: "MUR"},
	{Code: "SCR", Name: "塞舌尔卢比", Symbol: "SCR"},
	{Code: "CVE", Name: "佛得角埃斯库多", Symbol: "CVE"},
	{Code: "GMD", Name: "冈比亚达拉西", Symbol: "GMD"},
	{Code: "GNF", Name: "几内亚法郎", Symbol: "GNF"},
	{Code: "LRD", Name: "利比里亚元", Symbol: "L$"},
	{Code: "SLE", Name: "塞拉利昂利昂", Symbol: "SLE"},
	{Code: "STN", Name: "圣多美和普林西比多布拉", Symbol: "STN"},
	{Code: "MRU", Name: "毛里塔尼亚乌吉亚", Symbol: "MRU"},
	{Code: "SOS", Name: "索马里先令", Symbol: "SOS"},
	{Code: "SDG", Name: "苏丹镑", Symbol: "SDG"},
	{Code: "XOF", Name: "西非法郎", Symbol: "CFA"},
	{Code: "XAF", Name: "中非法郎", Symbol: "FCFA"},
	{Code: "XPF", Name: "太平洋法郎", Symbol: "CFPF"},
	{Code: "FJD", Name: "斐济元", Symbol: "FJ$"},
	{Code: "PGK", Name: "巴布亚新几内亚基那", Symbol: "PGK"},
	{Code: "WST", Name: "萨摩亚塔拉", Symbol: "WST"},
	{Code: "TOP", Name: "汤加潘加", Symbol: "TOP"},
	{Code: "VUV", Name: "瓦努阿图瓦图", Symbol: "VUV"},
	{Code: "SBD", Name: "所罗门群岛元", Symbol: "SI$"},
}

var byCode = func() map[string]Currency {
	index := make(map[string]Currency, len(currencies))
	for _, c := range currencies {
		index[c.Code] = c
	}
	return index
}()

// All 返回全部受支持的币种(调用方不得修改返回的切片)。
func All() []Currency { return currencies }

// Normalize 把币种代码转为大写并去掉空白。
func Normalize(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// IsSupported 判断币种代码(需已规范化)是否受支持。
func IsSupported(code string) bool {
	_, ok := byCode[code]
	return ok
}

// Symbol 返回币种符号,未知币种返回空。
func Symbol(code string) string { return byCode[code].Symbol }

// Name 返回币种的中文名称,未知币种返回空。
func Name(code string) string { return byCode[code].Name }
