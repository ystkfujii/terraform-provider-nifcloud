package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/nifcloud/nifcloud-sdk-go/service/hatoba"
	"github.com/nifcloud/terraform-provider-nifcloud/nifcloud/client"
)

const asyncActionWaitDelay = 15 // 15sec

func update(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	svc := meta.(*client.Client).Hatoba
	deadline, _ := ctx.Deadline()

	if d.IsNewResource() {
		err := hatoba.NewClusterRunningWaiter(svc).Wait(ctx, expandGetClusterInput(d), time.Until(deadline))
		if err != nil {
			return diag.FromErr(fmt.Errorf("failed waiting for Hatoba cluster to become ready: %s", err))
		}

		return read(ctx, d, meta)
	}

	if d.HasChanges("name", "description", "kubernetes_version", "addons_config") {
		input := expandUpdateClusterInput(d)
		_, err := svc.UpdateCluster(ctx, input)
		if err != nil {
			return diag.FromErr(fmt.Errorf("failed updating Hatoba cluster: %s", err))
		}

		d.SetId(d.Get("name").(string))

		// lintignore:R018
		time.Sleep(asyncActionWaitDelay * time.Second)

		err = hatoba.NewClusterRunningWaiter(svc).Wait(ctx, expandGetClusterInput(d), time.Until(deadline))
		if err != nil {
			return diag.FromErr(fmt.Errorf("failed waiting for Hatoba cluster to become ready: %s", err))
		}
	}

	if d.HasChange("node_pools") {
		o, n := d.GetChange("node_pools")
		toDeleteCandidate := o.(*schema.Set).Difference(n.(*schema.Set))
		toCreateCandidate := n.(*schema.Set).Difference(o.(*schema.Set))

		var toCreate []interface{}
		var toDelete []interface{}
		var toChangeSize []interface{}
		if toDeleteCandidate.Len() != 0 && toCreateCandidate.Len() != 0 {
			toChangeSize = detectNodeCountChangedNodePools(toDeleteCandidate, toCreateCandidate)
			toCreate = excludeNodePools(toCreateCandidate, toChangeSize)
			toDelete = excludeNodePools(toDeleteCandidate, toChangeSize)
		} else {
			toCreate = toCreateCandidate.List()
			toDelete = toDeleteCandidate.List()
		}

		for _, elm := range toChangeSize {
			input := expandSetNodePoolSizeInput(d, elm.(map[string]interface{}))
			_, err := svc.SetNodePoolSize(ctx, input)
			if err != nil {
				return diag.Errorf(err.Error())
			}

			// lintignore:R018
			time.Sleep(asyncActionWaitDelay * time.Second)

			if err := hatoba.NewClusterRunningWaiter(svc).Wait(ctx, expandGetClusterInput(d), time.Until(deadline)); err != nil {
				return diag.FromErr(fmt.Errorf("failed wait Hatoba cluster available: %s", err))
			}
		}

		for _, elm := range toCreate {
			input := expandCreateNodePoolInput(d, elm.(map[string]interface{}))
			_, err := svc.CreateNodePool(ctx, input)
			if err != nil {
				return diag.FromErr(fmt.Errorf("failed creating Hatoba cluster node pool: %s", err))
			}

			// lintignore:R018
			time.Sleep(asyncActionWaitDelay * time.Second)

			if err := hatoba.NewClusterRunningWaiter(svc).Wait(ctx, expandGetClusterInput(d), time.Until(deadline)); err != nil {
				return diag.FromErr(fmt.Errorf("failed wait Hatoba cluster available: %s", err))
			}
		}

		toDeleteNames := []string{}
		for _, elm := range toDelete {
			target := elm.(map[string]interface{})
			toDeleteNames = append(toDeleteNames, target["name"].(string))
		}
		if len(toDeleteNames) != 0 {
			deleteNodePoolsInput := expandDeleteNodePoolsInput(d, toDeleteNames)
			_, err := svc.DeleteNodePools(ctx, deleteNodePoolsInput)
			if err != nil {
				return diag.FromErr(fmt.Errorf("failed deleting Hatoba cluster node pools: %s", err))
			}

			// lintignore:R018
			time.Sleep(asyncActionWaitDelay * time.Second)

			if err := hatoba.NewClusterRunningWaiter(svc).Wait(ctx, expandGetClusterInput(d), time.Until(deadline)); err != nil {
				return diag.FromErr(fmt.Errorf("failed wait Hatoba cluster available: %s", err))
			}
		}
	}

	return read(ctx, d, meta)
}

func detectNodeCountChangedNodePools(deleteCandidate, createCandidate *schema.Set) []interface{} {
	res := []interface{}{}
	for _, d := range deleteCandidate.List() {
		td := d.(map[string]interface{})
		for _, a := range createCandidate.List() {
			ta := a.(map[string]interface{})
			if ta["name"] == td["name"] &&
				ta["instance_type"] == td["instance_type"] &&
				ta["node_count"] != td["node_count"] {
				res = append(res, a)
				break
			}
		}
	}

	return res
}

func excludeNodePools(from *schema.Set, targets []interface{}) []interface{} {
	res := []interface{}{}
	for _, f := range from.List() {
		fromElem := f.(map[string]interface{})
		found := false
		for _, t := range targets {
			targetElem := t.(map[string]interface{})
			if targetElem["name"] == fromElem["name"] &&
				targetElem["instance_type"] == fromElem["instance_type"] {
				found = true
				break
			}
		}

		if !found {
			res = append(res, f)
		}
	}

	return res
}
