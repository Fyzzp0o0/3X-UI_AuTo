package controller

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/secret"
	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	chainsvc "github.com/mhsanaei/3x-ui/v3/internal/web/service/chain"
)

// ChainController exposes the chain-proxy module over the panel API. It lives
// inside the authenticated /panel/api group, so auth, scopes, the config
// envelope and CSRF all apply without extra code here.
type ChainController struct {
	svc *chainsvc.Service
}

// NewChainController mounts /panel/api/chain routes on g.
func NewChainController(g *gin.RouterGroup) *ChainController {
	a := &ChainController{svc: chainsvc.NewService()}
	a.initRouter(g)
	return a
}

func (a *ChainController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/chain")
	g.GET("/meta", a.meta)

	g.GET("/pools", a.listPools)
	g.POST("/pools", a.createPool)
	g.GET("/pools/:id", a.getPool)
	g.POST("/pools/:id", a.updatePool)
	g.POST("/pools/:id/delete", a.deletePool)

	g.POST("/pools/:id/import", chainLimit("import", 20, 5), a.importProxies)
	g.GET("/pools/:id/proxies", a.listProxies)
	g.POST("/pools/:id/proxies/bulk", a.bulkProxies)
	g.POST("/proxies/:id", a.updateProxy)
	g.POST("/proxies/:id/probe", chainLimit("proxy-probe", 30, 5), a.probeProxy)
	g.POST("/pools/:id/probe", chainLimit("probe", 20, 4), a.probePool)
	g.POST("/pools/:id/plan", a.planPool)
	g.POST("/pools/:id/apply", chainLimit("apply", 12, 3), a.applyPool)
	g.POST("/pools/:id/failover", chainLimit("failover", 12, 3), a.failoverPool)

	g.GET("/nodes", a.listNodes)
	g.GET("/nodes/:id", a.getNode)
	g.POST("/nodes/:id/enable", a.setNodeEnabled(true))
	g.POST("/nodes/:id/disable", a.setNodeEnabled(false))
	g.POST("/nodes/:id/rebind", a.rebindNode)
	g.POST("/nodes/:id/delete", a.deleteNode)
	g.POST("/nodes/bulk", a.bulkNodes)
	g.POST("/nodes/export", chainLimit("export", 20, 5), a.exportNodes)

	g.GET("/audit", a.listAudit)
	g.GET("/jobs", a.listJobs)
	g.GET("/jobs/:id", a.getJob)
}

func (a *ChainController) meta(c *gin.Context) {
	jsonObj(c, gin.H{
		"enabled":     config.IsChainEnabled(),
		"encryption":  secret.Enabled(),
		"maxImport":   chainsvc.MaxImportLines,
		"modes":       []string{"shared", "sharded", "dedicated"},
		"protocols":   []string{"vless"},
		"defaultPort": 2083,
	}, nil)
}

func (a *ChainController) listPools(c *gin.Context) {
	pools, err := a.svc.ListPools()
	jsonObj(c, pools, err)
}

func (a *ChainController) createPool(c *gin.Context) {
	var in chainsvc.PoolInput
	if err := c.ShouldBindJSON(&in); err != nil {
		jsonMsg(c, "", common.NewError("invalid pool payload:", err))
		return
	}
	pool, err := a.svc.CreatePool(in)
	jsonObj(c, pool, err)
}

func (a *ChainController) getPool(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	pool, err := a.svc.GetPool(id)
	jsonObj(c, pool, err)
}

func (a *ChainController) updatePool(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	var in chainsvc.PoolInput
	if err := c.ShouldBindJSON(&in); err != nil {
		jsonMsg(c, "", common.NewError("invalid pool payload:", err))
		return
	}
	jsonObj(c, nil, a.svc.UpdatePool(id, in))
}

func (a *ChainController) deletePool(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	err := a.svc.DeletePool(id)
	if err == nil {
		a.audit(c, "pool.delete", "pool", id, "")
	}
	jsonObj(c, nil, err)
}

type chainImportRequest struct {
	Content      string `json:"content"`
	DryRun       bool   `json:"dryRun"`
	AllowPrivate bool   `json:"allowPrivate"`
}

func (a *ChainController) importProxies(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	var req chainImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, "", common.NewError("invalid import payload:", err))
		return
	}
	res, err := a.svc.ImportProxies(c.Request.Context(), id, req.Content,
		chainsvc.ImportOptions{DryRun: req.DryRun, AllowPrivate: req.AllowPrivate})
	if err == nil && !req.DryRun {
		a.audit(c, "proxy.import", "pool", id, fmt.Sprintf("created=%d duplicate=%d", res.Created, res.Duplicate))
	}
	jsonObj(c, res, err)
}

func (a *ChainController) listProxies(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	rows, total, err := a.svc.ListProxies(id, chainsvc.ProxyQuery{
		Page: queryInt(c, "page", 1), Size: queryInt(c, "size", 50),
		Status: c.Query("status"), Keyword: c.Query("keyword"),
	})
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, gin.H{"items": rows, "total": total, "page": queryInt(c, "page", 1), "size": queryInt(c, "size", 50)}, nil)
}

type chainBulkRequest struct {
	IDs    []int  `json:"ids"`
	Action string `json:"action"`
}

func (a *ChainController) bulkProxies(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	var req chainBulkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, "", common.NewError("invalid bulk payload:", err))
		return
	}
	action := chainsvc.BulkProxyAction(req.Action)
	switch action {
	case chainsvc.ProxyEnable, chainsvc.ProxyDisable, chainsvc.ProxyDelete:
	default:
		jsonMsg(c, "", common.NewError("action must be enable, disable or delete"))
		return
	}
	affected, err := a.svc.BulkProxies(id, req.IDs, action)
	jsonObj(c, gin.H{"affected": affected}, err)
}

func (a *ChainController) probePool(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	job, err := a.svc.StartProbeJob(id, "api")
	if err == nil {
		a.audit(c, "pool.probe", "pool", id, fmt.Sprintf("job=%d", job.Id))
	}
	jsonObj(c, job, err)
}

func (a *ChainController) planPool(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	var opt chainsvc.ApplyOptions
	_ = c.ShouldBindJSON(&opt)
	plan, err := a.svc.Plan(id, opt)
	jsonObj(c, plan, err)
}

func (a *ChainController) applyPool(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	var opt chainsvc.ApplyOptions
	_ = c.ShouldBindJSON(&opt)
	job, err := a.svc.StartApplyJob(id, opt, "api")
	if err == nil {
		a.audit(c, "pool.apply", "pool", id, fmt.Sprintf("job=%d", job.Id))
	}
	jsonObj(c, job, err)
}

func (a *ChainController) failoverPool(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	job, err := a.svc.StartFailoverJob(id, "api")
	if err == nil {
		a.audit(c, "pool.failover", "pool", id, fmt.Sprintf("job=%d", job.Id))
	}
	jsonObj(c, job, err)
}

func (a *ChainController) rebindNode(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	err := a.svc.RebindNode(id)
	if err == nil {
		a.audit(c, "node.rebind", "node", id, "")
	}
	jsonObj(c, nil, err)
}

type chainBulkNodesRequest struct {
	IDs    []int  `json:"ids"`
	Action string `json:"action"`
}

func (a *ChainController) bulkNodes(c *gin.Context) {
	var req chainBulkNodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, "", common.NewError("invalid bulk payload:", err))
		return
	}
	action := chainsvc.BulkNodeAction(req.Action)
	switch action {
	case chainsvc.NodeEnable, chainsvc.NodeDisable, chainsvc.NodeDelete, chainsvc.NodeRebind:
	default:
		jsonMsg(c, "", common.NewError("action must be enable, disable, delete or rebind"))
		return
	}
	affected, err := a.svc.BulkNodes(req.IDs, action)
	if err == nil {
		a.audit(c, "node.bulk."+req.Action, "node", 0, fmt.Sprintf("affected=%d", affected))
	}
	jsonObj(c, gin.H{"affected": affected}, err)
}

type chainExportRequest struct {
	IDs    []int `json:"ids"`
	PoolID int   `json:"poolId"`
}

func (a *ChainController) exportNodes(c *gin.Context) {
	var req chainExportRequest
	_ = c.ShouldBindJSON(&req)
	links, err := a.svc.ExportLinks(req.IDs, req.PoolID, resolveHost(c))
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	a.audit(c, "node.export", "node", 0, fmt.Sprintf("count=%d", len(links)))
	jsonObj(c, gin.H{"count": len(links), "links": links}, nil)
}

type chainProxyRequest struct {
	Host         string  `json:"host"`
	Port         int     `json:"port"`
	Username     string  `json:"username"`
	Password     *string `json:"password"`
	Label        string  `json:"label"`
	AllowPrivate bool    `json:"allowPrivate"`
	Enabled      *bool   `json:"enabled"`
}

func (a *ChainController) updateProxy(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	var req chainProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, "", common.NewError("invalid proxy payload:", err))
		return
	}
	err := a.svc.UpdateProxy(id, chainsvc.ProxyInput{
		Host: req.Host, Port: req.Port, Username: req.Username, Password: req.Password,
		Label: req.Label, AllowPrivate: req.AllowPrivate, Enabled: req.Enabled,
	})
	if err == nil {
		a.audit(c, "proxy.update", "proxy", id, fmt.Sprintf("%s:%d", req.Host, req.Port))
	}
	jsonObj(c, nil, err)
}

func (a *ChainController) probeProxy(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	res, err := a.svc.ProbeProxy(c.Request.Context(), id)
	if err == nil {
		a.audit(c, "proxy.probe", "proxy", id, "")
	}
	jsonObj(c, res, err)
}

func (a *ChainController) deleteNode(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	err := a.svc.DeleteNode(id)
	if err == nil {
		a.audit(c, "node.delete", "node", id, "")
	}
	jsonObj(c, nil, err)
}

func (a *ChainController) listAudit(c *gin.Context) {
	rows, total, err := a.svc.ListAudit(queryInt(c, "page", 1), queryInt(c, "size", 50))
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, gin.H{"items": rows, "total": total}, nil)
}

// audit records a security-relevant action; failures never fail the request.
func (a *ChainController) audit(c *gin.Context, action, object string, id int, detail string) {
	actor := "session"
	if c.GetBool("api_authed") {
		actor = "api:" + c.GetString("api_token_scope")
	}
	_ = a.svc.Audit(actor, c.ClientIP(), c.Request.UserAgent(), chainsvc.AuditRecord{
		Action: action, ObjectType: object, ObjectID: id, Detail: detail,
	})
}

func (a *ChainController) listNodes(c *gin.Context) {
	rows, total, err := a.svc.ListNodes(chainsvc.NodeQuery{
		Page: queryInt(c, "page", 1), Size: queryInt(c, "size", 50),
		PoolID: queryInt(c, "poolId", 0), Status: c.Query("status"), Keyword: c.Query("keyword"),
	})
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, gin.H{"items": rows, "total": total}, nil)
}

func (a *ChainController) getNode(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	node, hops, err := a.svc.GetNode(id)
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	up, down, _ := a.svc.NodeTraffic(node.Email)
	jsonObj(c, gin.H{
		"node": node, "hops": hops, "links": a.svc.NodeLinks(id, resolveHost(c)),
		"traffic": gin.H{"up": up, "down": down},
	}, nil)
}

func (a *ChainController) setNodeEnabled(enable bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := chainErrorID(c)
		if !ok {
			return
		}
		node, _, err := a.svc.GetNode(id)
		if err != nil {
			jsonObj(c, nil, err)
			return
		}
		jsonObj(c, nil, a.svc.SetNodeEnabled(node.PoolId, id, enable))
	}
}

func (a *ChainController) listJobs(c *gin.Context) {
	jobs, err := a.svc.ListJobs(queryInt(c, "poolId", 0), queryInt(c, "limit", 50))
	jsonObj(c, jobs, err)
}

func (a *ChainController) getJob(c *gin.Context) {
	id, ok := chainErrorID(c)
	if !ok {
		return
	}
	job, err := a.svc.GetJob(id)
	jsonObj(c, job, err)
}

func chainErrorID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		jsonMsg(c, "", common.NewError("invalid id"))
		return 0, false
	}
	return id, true
}

func queryInt(c *gin.Context, name string, fallback int) int {
	raw := c.Query(name)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}
