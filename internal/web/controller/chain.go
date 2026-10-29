package controller

import (
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

	g.POST("/pools/:id/import", a.importProxies)
	g.GET("/pools/:id/proxies", a.listProxies)
	g.POST("/pools/:id/proxies/bulk", a.bulkProxies)
	g.POST("/pools/:id/probe", a.probePool)
	g.POST("/pools/:id/plan", a.planPool)
	g.POST("/pools/:id/apply", a.applyPool)

	g.GET("/nodes", a.listNodes)
	g.GET("/nodes/:id", a.getNode)
	g.POST("/nodes/:id/enable", a.setNodeEnabled(true))
	g.POST("/nodes/:id/disable", a.setNodeEnabled(false))

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
	jsonObj(c, nil, a.svc.DeletePool(id))
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
	jsonObj(c, job, err)
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
	jsonObj(c, gin.H{"node": node, "hops": hops}, nil)
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
