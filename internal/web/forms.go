package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"mb/internal/asset"
	"mb/internal/money"
	"mb/internal/store"
	"mb/internal/usererr"
)

// 文本字段的长度上限(按字符计)。
const (
	maxShortText = 255
	maxLongText  = 4000

	maxBillingMonths = 1200
)

// ---- 域名表单 ----

// domainForm 保存表单的原始输入,校验失败时原样回填。
type domainForm struct {
	Name        string
	Registrar   string
	ExpiresAt   string
	Permanent   bool
	Price       string
	Currency    string
	Notes       string
	ExcludeCost bool
	Pinned      bool
	Hidden      bool
}

type domainFormPage struct {
	base
	ID         int64 // 0 表示新增
	Action     string
	Error      string
	Form       domainForm
	Currencies []money.Currency
}

func readDomainForm(r *http.Request) domainForm {
	f := r.PostForm
	return domainForm{
		Name:        strings.TrimSpace(f.Get("domain")),
		Registrar:   strings.TrimSpace(f.Get("registrar")),
		ExpiresAt:   strings.TrimSpace(f.Get("expires_at")),
		Permanent:   f.Get("permanent") == "1",
		Price:       strings.TrimSpace(f.Get("price")),
		Currency:    money.Normalize(f.Get("currency")),
		Notes:       strings.TrimSpace(f.Get("notes")),
		ExcludeCost: f.Get("exclude_cost") == "1",
		Pinned:      f.Get("pinned") == "1",
		Hidden:      f.Get("hidden") == "1",
	}
}

func domainFormOf(d asset.Domain) domainForm {
	form := domainForm{
		Name: d.Name, Registrar: d.Registrar, ExpiresAt: d.ExpiresAt,
		Price: money.FormatExact(d.Price), Currency: d.Currency, Notes: d.Notes,
		ExcludeCost: d.ExcludeCost, Pinned: d.Pinned, Hidden: d.Hidden,
	}
	if asset.IsPermanent(d.ExpiresAt) {
		form.Permanent, form.ExpiresAt = true, ""
	}
	return form
}

// domain 校验表单并转换为域名记录;返回的错误都是面向用户的提示。
func (f domainForm) domain() (asset.Domain, error) {
	name := cleanDomainName(f.Name)
	switch {
	case name == "":
		return asset.Domain{}, usererr.New("域名不能为空")
	case strings.ContainsFunc(name, unicode.IsSpace):
		return asset.Domain{}, usererr.New("域名中不能包含空格")
	}
	if err := checkLengths(
		textField{"域名", name, maxShortText},
		textField{"注册商", f.Registrar, maxShortText},
		textField{"备注", f.Notes, maxLongText},
	); err != nil {
		return asset.Domain{}, err
	}
	expires, err := parseExpiryInput(f.ExpiresAt, f.Permanent)
	if err != nil {
		return asset.Domain{}, err
	}
	price, currency, err := parsePriceInput(f.Price, f.Currency)
	if err != nil {
		return asset.Domain{}, err
	}
	return asset.Domain{
		Name: name, Registrar: f.Registrar, ExpiresAt: expires,
		Price: price, Currency: currency, Notes: f.Notes,
		ExcludeCost: f.ExcludeCost, Pinned: f.Pinned, Hidden: f.Hidden,
	}, nil
}

// cleanDomainName 允许用户直接粘贴网址:去掉协议前缀、路径和末尾的点。
func cleanDomainName(raw string) string {
	name := strings.TrimSpace(raw)
	if u, err := url.Parse(name); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		name = u.Host
	}
	name, _, _ = strings.Cut(name, "/")
	return strings.TrimSuffix(name, ".")
}

func (s *Server) renderDomainForm(w http.ResponseWriter, r *http.Request, status int, id int64, form domainForm, message string) {
	p := &domainFormPage{
		base:       base{Title: "新增域名"},
		ID:         id,
		Action:     "/domains/new",
		Error:      message,
		Form:       form,
		Currencies: money.All(),
	}
	if id > 0 {
		p.Title = "编辑域名"
		p.Action = "/domains/" + strconv.FormatInt(id, 10) + "/edit"
	}
	s.render(w, r, status, "domain_form", p)
}

func (s *Server) handleDomainNew(w http.ResponseWriter, r *http.Request) {
	s.renderDomainForm(w, r, http.StatusOK, 0, domainForm{
		ExpiresAt: oneYearFromNow(),
		Currency:  money.DefaultCurrency,
	}, "")
}

func (s *Server) handleDomainCreate(w http.ResponseWriter, r *http.Request) {
	form := readDomainForm(r)
	d, err := form.domain()
	if err != nil {
		s.renderDomainForm(w, r, http.StatusUnprocessableEntity, 0, form, err.Error())
		return
	}
	if _, err := s.store.CreateDomain(r.Context(), d); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirectWithFlash(w, r, "/#domain", flashOK, "已添加域名 "+d.Name)
}

func (s *Server) handleDomainEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	d, err := s.store.Domain(r.Context(), id)
	if err != nil {
		s.assetError(w, r, err)
		return
	}
	s.renderDomainForm(w, r, http.StatusOK, id, domainFormOf(d), "")
}

func (s *Server) handleDomainUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	form := readDomainForm(r)
	d, err := form.domain()
	if err != nil {
		s.renderDomainForm(w, r, http.StatusUnprocessableEntity, id, form, err.Error())
		return
	}
	d.ID = id
	if err := s.store.UpdateDomain(r.Context(), d); err != nil {
		s.assetError(w, r, err)
		return
	}
	s.redirectWithFlash(w, r, "/#domain", flashOK, "已保存域名 "+d.Name)
}

// ---- VPS 表单 ----

type diskInput struct {
	Size string
	Type string
}

type vpsForm struct {
	Vendor string
	Region string
	IP     string
	Specs  string

	Cores    string
	MemoryGB string
	Disks    []diskInput

	TrafficTB      string
	TrafficDir     string
	BandwidthValue string
	BandwidthUnit  string

	ExpiresAt     string
	Permanent     bool
	Price         string
	Currency      string
	BillingMonths string
	Notes         string
	ExcludeCost   bool
	Pinned        bool
	Hidden        bool
}

type vpsFormPage struct {
	base
	ID         int64
	Action     string
	Error      string
	Form       vpsForm
	Currencies []money.Currency
}

func readVPSForm(r *http.Request) vpsForm {
	f := r.PostForm
	form := vpsForm{
		Vendor:         strings.TrimSpace(f.Get("vendor")),
		Region:         strings.TrimSpace(f.Get("region")),
		IP:             strings.TrimSpace(f.Get("ip")),
		Specs:          strings.TrimSpace(f.Get("specs")),
		Cores:          strings.TrimSpace(f.Get("cores")),
		MemoryGB:       strings.TrimSpace(f.Get("memory_gb")),
		TrafficTB:      strings.TrimSpace(f.Get("traffic_tb")),
		TrafficDir:     strings.TrimSpace(f.Get("traffic_dir")),
		BandwidthValue: strings.TrimSpace(f.Get("bandwidth_value")),
		BandwidthUnit:  strings.TrimSpace(f.Get("bandwidth_unit")),
		ExpiresAt:      strings.TrimSpace(f.Get("expires_at")),
		Permanent:      f.Get("permanent") == "1",
		Price:          strings.TrimSpace(f.Get("price")),
		Currency:       money.Normalize(f.Get("currency")),
		BillingMonths:  strings.TrimSpace(f.Get("billing_months")),
		Notes:          strings.TrimSpace(f.Get("notes")),
		ExcludeCost:    f.Get("exclude_cost") == "1",
		Pinned:         f.Get("pinned") == "1",
		Hidden:         f.Get("hidden") == "1",
	}

	// 硬盘是可增减的多行输入,容量与类型按行一一对应;空行直接丢弃。
	sizes, types := f["disk_gb"], f["disk_type"]
	for i := range max(len(sizes), len(types)) {
		var disk diskInput
		if i < len(sizes) {
			disk.Size = strings.TrimSpace(sizes[i])
		}
		if i < len(types) {
			disk.Type = strings.TrimSpace(types[i])
		}
		if disk != (diskInput{}) {
			form.Disks = append(form.Disks, disk)
		}
	}
	return form.withDiskRow()
}

// withDiskRow 保证表单里至少有一行硬盘输入。
func (f vpsForm) withDiskRow() vpsForm {
	if len(f.Disks) == 0 {
		f.Disks = []diskInput{{}}
	}
	return f
}

func vpsFormOf(v asset.VPS) vpsForm {
	form := vpsForm{
		Vendor: v.Vendor, Region: v.Region, IP: v.IP, Specs: v.Specs,
		Cores: positiveOrEmpty(float64(v.Cores)), MemoryGB: positiveOrEmpty(v.MemoryGB),
		TrafficDir:     v.TrafficDir,
		BandwidthValue: positiveOrEmpty(v.BandwidthValue), BandwidthUnit: v.BandwidthUnit,
		ExpiresAt: v.ExpiresAt, Price: money.FormatExact(v.Price), Currency: v.Currency,
		BillingMonths: strconv.Itoa(v.BillingMonths), Notes: v.Notes,
		ExcludeCost: v.ExcludeCost, Pinned: v.Pinned, Hidden: v.Hidden,
	}
	for _, d := range v.Disks {
		form.Disks = append(form.Disks, diskInput{Size: strconv.Itoa(d.SizeGB), Type: d.Type})
	}
	if v.TrafficSet {
		form.TrafficTB = money.FormatExact(v.TrafficTB) // 0 表示不限流量,需要原样显示
	}
	if asset.IsPermanent(v.ExpiresAt) {
		form.Permanent, form.ExpiresAt = true, ""
	}
	return form.withDiskRow()
}

func positiveOrEmpty(f float64) string {
	if f <= 0 {
		return ""
	}
	return money.FormatExact(f)
}

// vps 校验表单并转换为 VPS 记录;返回的错误都是面向用户的提示。
func (f vpsForm) vps() (asset.VPS, error) {
	fail := func(message string) (asset.VPS, error) { return asset.VPS{}, usererr.New(message) }

	if f.Vendor == "" {
		return fail("厂商不能为空")
	}
	if err := checkLengths(
		textField{"厂商", f.Vendor, maxShortText},
		textField{"地区", f.Region, maxShortText},
		textField{"IP", f.IP, maxShortText},
		textField{"自定义描述", f.Specs, maxLongText},
		textField{"备注", f.Notes, maxLongText},
	); err != nil {
		return asset.VPS{}, err
	}

	v := asset.VPS{
		Vendor: f.Vendor, Region: f.Region, IP: f.IP, Specs: f.Specs, Notes: f.Notes,
		ExcludeCost: f.ExcludeCost, Pinned: f.Pinned, Hidden: f.Hidden,
		BandwidthUnit: asset.NormalizeBandwidthUnit(f.BandwidthUnit),
		BillingMonths: asset.DefaultBillingMonths,
	}

	var err error
	if f.Cores != "" {
		if v.Cores, err = strconv.Atoi(f.Cores); err != nil || v.Cores < 0 {
			return fail("核心数应为非负整数")
		}
	}
	if v.MemoryGB, err = optionalNumber(f.MemoryGB); err != nil {
		return fail("内存应为非负数字")
	}

	for i, d := range f.Disks {
		if d == (diskInput{}) {
			continue
		}
		size, err := strconv.Atoi(d.Size)
		switch {
		case d.Size == "":
			return asset.VPS{}, usererr.Newf("第 %d 块硬盘选择了类型但未填写容量", i+1)
		case err != nil || size <= 0:
			return asset.VPS{}, usererr.Newf("第 %d 块硬盘的容量应为正整数", i+1)
		case d.Type != "" && asset.NormalizeDiskType(d.Type) == "":
			return asset.VPS{}, usererr.Newf("第 %d 块硬盘的类型仅支持 SSD / HDD", i+1)
		}
		v.Disks = append(v.Disks, asset.Disk{SizeGB: size, Type: asset.NormalizeDiskType(d.Type)})
	}
	if len(v.Disks) > asset.MaxDisks {
		return asset.VPS{}, usererr.Newf("最多支持 %d 块硬盘", asset.MaxDisks)
	}

	v.TrafficDir = asset.NormalizeTrafficDir(f.TrafficDir)
	switch {
	case f.TrafficDir != "" && v.TrafficDir == "":
		return fail("单双向选项不正确")
	case f.TrafficTB == "" && v.TrafficDir != "":
		return fail("要标注单向 / 双向，请先填写流量（0 表示不限）")
	case f.TrafficTB != "":
		v.TrafficSet = true
		if v.TrafficTB, err = optionalNumber(f.TrafficTB); err != nil {
			return fail("流量应为非负数字")
		}
	}

	if v.BandwidthValue, err = optionalNumber(f.BandwidthValue); err != nil {
		return fail("带宽应为非负数字")
	}
	if f.BandwidthUnit != "" && !strings.EqualFold(f.BandwidthUnit, asset.UnitMbps) && !strings.EqualFold(f.BandwidthUnit, asset.UnitGbps) {
		return fail("带宽单位仅支持 Mbps / Gbps")
	}

	if v.ExpiresAt, err = parseExpiryInput(f.ExpiresAt, f.Permanent); err != nil {
		return asset.VPS{}, err
	}
	if v.Price, v.Currency, err = parsePriceInput(f.Price, f.Currency); err != nil {
		return asset.VPS{}, err
	}
	if f.BillingMonths != "" {
		if v.BillingMonths, err = parseMonths(f.BillingMonths); err != nil {
			return asset.VPS{}, err
		}
	}
	return v, nil
}

func (s *Server) renderVPSForm(w http.ResponseWriter, r *http.Request, status int, id int64, form vpsForm, message string) {
	p := &vpsFormPage{
		base:       base{Title: "新增 VPS"},
		ID:         id,
		Action:     "/vps/new",
		Error:      message,
		Form:       form,
		Currencies: money.All(),
	}
	if id > 0 {
		p.Title = "编辑 VPS"
		p.Action = "/vps/" + strconv.FormatInt(id, 10) + "/edit"
	}
	s.render(w, r, status, "vps_form", p)
}

func (s *Server) handleVPSNew(w http.ResponseWriter, r *http.Request) {
	s.renderVPSForm(w, r, http.StatusOK, 0, vpsForm{
		ExpiresAt:     oneYearFromNow(),
		Currency:      money.DefaultCurrency,
		BandwidthUnit: asset.UnitMbps,
		BillingMonths: strconv.Itoa(asset.DefaultBillingMonths),
	}.withDiskRow(), "")
}

func (s *Server) handleVPSCreate(w http.ResponseWriter, r *http.Request) {
	form := readVPSForm(r)
	v, err := form.vps()
	if err != nil {
		s.renderVPSForm(w, r, http.StatusUnprocessableEntity, 0, form, err.Error())
		return
	}
	if _, err := s.store.CreateVPS(r.Context(), v); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirectWithFlash(w, r, "/#vps", flashOK, "已添加 VPS "+v.Vendor)
}

func (s *Server) handleVPSEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	v, err := s.store.VPS(r.Context(), id)
	if err != nil {
		s.assetError(w, r, err)
		return
	}
	s.renderVPSForm(w, r, http.StatusOK, id, vpsFormOf(v), "")
}

func (s *Server) handleVPSUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	form := readVPSForm(r)
	v, err := form.vps()
	if err != nil {
		s.renderVPSForm(w, r, http.StatusUnprocessableEntity, id, form, err.Error())
		return
	}
	v.ID = id
	if err := s.store.UpdateVPS(r.Context(), v); err != nil {
		s.assetError(w, r, err)
		return
	}
	s.redirectWithFlash(w, r, "/#vps", flashOK, "已保存 VPS "+v.Vendor)
}

// ---- 共用的解析与校验 ----

// pathID 读取路由中的 {id}。
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// assetError 处理读取 / 更新单条资产时的错误:记录不存在返回 404,其余视为内部错误。
func (s *Server) assetError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "记录不存在，可能已被删除")
		return
	}
	s.serverError(w, r, err)
}

func oneYearFromNow() string {
	return time.Now().AddDate(1, 0, 0).Format(asset.DateLayout)
}

func parseExpiryInput(raw string, permanent bool) (string, error) {
	if permanent {
		return asset.Permanent, nil
	}
	return asset.ParseExpiry(raw)
}

func parsePriceInput(price, currency string) (float64, string, error) {
	amount, err := money.ParseAmount(price)
	if err != nil {
		return 0, "", usererr.New("续费价格应为非负数字，免费可填 0")
	}
	if !money.IsSupported(currency) {
		return 0, "", usererr.New("币种不受支持，请从下拉列表中选择")
	}
	return amount, currency, nil
}

// parseMonths 解析以月为单位的周期。
func parseMonths(raw string) (int, error) {
	months, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || months <= 0 || months > maxBillingMonths {
		return 0, usererr.Newf("月数应为 1-%d 之间的整数", maxBillingMonths)
	}
	return months, nil
}

// optionalNumber 解析可留空的非负数字,留空视为 0。
func optionalNumber(raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || !money.NonNegative(f) {
		return 0, errors.New("not a non-negative number")
	}
	return f, nil
}

type textField struct {
	label string
	value string
	limit int
}

func checkLengths(fields ...textField) error {
	for _, f := range fields {
		if utf8.RuneCountInString(f.value) > f.limit {
			return usererr.Newf("%s过长，最多 %d 个字符", f.label, f.limit)
		}
	}
	return nil
}
