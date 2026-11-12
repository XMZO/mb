package web

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mb/internal/asset"
	"mb/internal/money"
	"mb/internal/store"
	"mb/internal/usererr"
)

// maxBatchIDs 是一次批量操作最多涉及的记录数。
const maxBatchIDs = 10000

// batchRequest 是批量操作的请求体。单条记录的操作(置顶、续费、删除)也走这个接口,ids 里只放一个 ID。
type batchRequest struct {
	IDs    []int64 `json:"ids"`
	Action string  `json:"action"`
	Value  string  `json:"value"`
}

// handleBatch 处理 POST /api/{kind}/batch。
func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	kind, ok := asset.ParseKind(r.PathValue("kind"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "未知的资产类型")
		return
	}
	var req batchRequest
	if err := readJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	ids := cleanIDs(req.IDs)
	if len(ids) == 0 {
		writeJSONError(w, http.StatusBadRequest, "请先选择记录")
		return
	}
	if err := s.applyBatch(r.Context(), kind, ids, req.Action, req.Value); err != nil {
		apiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) applyBatch(ctx context.Context, kind asset.Kind, ids []int64, action, value string) error {
	switch action {
	case "delete":
		return s.store.DeleteAssets(ctx, kind, ids)
	case "pin", "unpin":
		return s.store.SetFlag(ctx, kind, ids, store.FlagPinned, action == "pin")
	case "hide", "unhide":
		return s.store.SetFlag(ctx, kind, ids, store.FlagHidden, action == "hide")
	case "exclude_cost", "include_cost":
		return s.store.SetFlag(ctx, kind, ids, store.FlagExcludeCost, action == "exclude_cost")
	case "renew": // 统一设为指定的到期日
		expires, err := asset.ParseExpiry(value)
		if err != nil {
			return err
		}
		return s.store.SetExpiry(ctx, kind, ids, expires)
	case "extend": // 各自顺延若干个月
		months, err := parseMonths(value)
		if err != nil {
			return err
		}
		return s.store.ExtendExpiry(ctx, kind, ids, months)
	case "billing_months":
		if kind != asset.KindVPS {
			return usererr.New("只有 VPS 可以设置付费周期")
		}
		months, err := parseMonths(value)
		if err != nil {
			return err
		}
		return s.store.SetBillingMonths(ctx, ids, months)
	}
	return usererr.New("不支持的操作")
}

// handleReorder 处理 POST /api/{kind}/order:按请求给出的 ID 顺序保存列表的默认排序。
func (s *Server) handleReorder(w http.ResponseWriter, r *http.Request) {
	kind, ok := asset.ParseKind(r.PathValue("kind"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "未知的资产类型")
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	ids := cleanIDs(req.IDs)
	if len(ids) == 0 {
		writeJSONError(w, http.StatusBadRequest, "排序列表为空")
		return
	}
	if err := s.store.Reorder(r.Context(), kind, ids); err != nil {
		apiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// cleanIDs 去掉非法与重复的 ID,保持原有顺序;超过上限的部分被截断。
func cleanIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] && len(out) < maxBatchIDs {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// apiError 把错误写成 JSON:用户错误原样返回,其余只记日志。
func apiError(w http.ResponseWriter, r *http.Request, err error) {
	if usererr.Is(err) {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	writeJSONError(w, http.StatusInternalServerError, "服务器内部错误，详情请查看服务端日志")
}

// ---- 域名导出 ----

// handleDomainExport 处理 POST /domains/export。
//
// 表单字段:format 为 txt(仅域名)或 csv(详细信息);scope 为 ids 时只导出 ids 字段
// 列出的记录并保持其顺序——"当前筛选""当前页""已选中"都由页面算好 ID 列表后提交。
func (s *Server) handleDomainExport(w http.ResponseWriter, r *http.Request) {
	domains, err := s.store.Domains(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if r.PostForm.Get("scope") == "ids" {
		domains = pickDomains(domains, r.PostForm.Get("ids"))
	}

	stamp := time.Now().Format("20060102-150405")
	if r.PostForm.Get("format") == "csv" {
		data, err := domainsCSV(domains)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		sendDownload(w, "mb-domains-"+stamp+".csv", "text/csv; charset=utf-8", data)
		return
	}

	names := make([]string, len(domains))
	for i, d := range domains {
		names[i] = d.Name
	}
	sendDownload(w, "mb-domains-"+stamp+".txt", "text/plain; charset=utf-8", []byte(strings.Join(names, "\n")+"\n"))
}

// pickDomains 按逗号分隔的 ID 列表挑选域名,结果的顺序与列表一致。
func pickDomains(all []asset.Domain, rawIDs string) []asset.Domain {
	byID := make(map[int64]asset.Domain, len(all))
	for _, d := range all {
		byID[d.ID] = d
	}
	var picked []asset.Domain
	for _, part := range strings.Split(rawIDs, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			continue
		}
		if d, ok := byID[id]; ok {
			picked = append(picked, d)
			delete(byID, id) // 同一条记录只导出一次
		}
	}
	return picked
}

func domainsCSV(domains []asset.Domain) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("ï»¿") // UTF-8 BOM,让 Excel 正确识别编码
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"domain", "registrar", "expires_at", "renewal_price", "currency", "notes", "exclude_cost", "pinned", "hidden"})
	for _, d := range domains {
		expires := d.ExpiresAt
		if asset.IsPermanent(expires) {
			expires = "永久"
		}
		_ = w.Write([]string{
			csvText(d.Name), csvText(d.Registrar), expires, money.FormatExact(d.Price), d.Currency, csvText(d.Notes),
			store.BoolValue(d.ExcludeCost), store.BoolValue(d.Pinned), store.BoolValue(d.Hidden),
		})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// csvText 防止表格软件把以 = + - @ 开头的单元格当作公式执行。
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func sendDownload(w http.ResponseWriter, filename, contentType string, data []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
