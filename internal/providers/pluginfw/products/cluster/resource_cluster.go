package cluster

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/apierr"
	"github.com/databricks/databricks-sdk-go/service/compute"
	"github.com/databricks/terraform-provider-databricks/clusters"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/converters"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/databricks/terraform-provider-databricks/internal/service/compute_tf"
	"github.com/databricks/terraform-provider-databricks/libraries"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

const resourceName = "cluster"
const clusterDefaultProvisionTimeout = 30 * time.Minute

var _ resource.ResourceWithConfigure = &ClusterResource{}
var _ resource.ResourceWithImportState = &ClusterResource{}

func ResourceCluster() resource.Resource {
	return &ClusterResource{}
}

type ClusterSpecExtended struct {
	compute_tf.ClusterSpec_SdkV2
	ID                     types.String `tfsdk:"id"`
	ClusterId              types.String `tfsdk:"cluster_id"`
	DefaultTags            types.Map    `tfsdk:"default_tags"`
	State                  types.String `tfsdk:"state"`
	Url                    types.String `tfsdk:"url"`
	IsPinned               types.Bool   `tfsdk:"is_pinned"`
	NoWait                 types.Bool   `tfsdk:"no_wait"`
	IdempotencyToken       types.String `tfsdk:"idempotency_token"`
	AutoterminationMinutes types.Int64  `tfsdk:"autotermination_minutes"`
	Library                types.List   `tfsdk:"library"`
	ClusterMountInfo       types.List   `tfsdk:"cluster_mount_info"`
	tfschema.Namespace_SdkV2
}

var _ pluginfwcommon.ComplexFieldTypeProvider = ClusterSpecExtended{}

func (c ClusterSpecExtended) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	attrs := c.ClusterSpec_SdkV2.GetComplexFieldTypes(ctx)
	attrs["provider_config"] = reflect.TypeOf(tfschema.ProviderConfig{})
	attrs["library"] = reflect.TypeOf(compute_tf.Library_SdkV2{})
	attrs["cluster_mount_info"] = reflect.TypeOf(clusters.MountInfo{})
	attrs["default_tags"] = reflect.TypeOf(types.String{})
	return attrs
}

type ClusterResource struct {
	Client *common.DatabricksClient
}

func (r *ClusterResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(resourceName)
}

func (r *ClusterResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs, blocks := tfschema.ResourceStructToSchemaMap(ctx, ClusterSpecExtended{}, func(c tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		c.ConfigureAsSdkV2Compatible()

		c.SetRequired("spark_version")
		c.SetComputed("enable_elastic_disk")
		c.SetComputed("enable_local_disk_encryption")
		c.SetComputed("node_type_id")
		c.SetComputed("driver_node_type_id")
		c.SetComputed("driver_instance_pool_id")

		c.SetOptional("id")
		c.SetComputed("id")
		c.SetComputed("cluster_id")
		c.SetComputed("default_tags")
		c.SetComputed("state")
		c.SetComputed("url")

		c.SetOptional("is_pinned")
		c.SetOptional("no_wait")
		c.SetOptional("idempotency_token")
		c.SetOptional("autotermination_minutes")
		c.SetOptional("library")
		c.SetOptional("cluster_mount_info")

		c.AddValidator(listvalidator.SizeAtMost(1), "provider_config")
		c.AddValidator(listvalidator.SizeAtMost(10), "ssh_public_keys")
		c.AddValidator(listvalidator.SizeAtMost(10), "init_scripts")

		c.SetDeprecated(clusters.DbfsDeprecationWarning, "init_scripts", "dbfs")
		c.SetRequired("init_scripts", "dbfs", "destination")
		c.SetRequired("init_scripts", "s3", "destination")
		c.SetRequired("init_scripts", "volumes", "destination")
		c.SetRequired("init_scripts", "workspace", "destination")

		c.SetRequired("workload_type", "clients")

		c.SetDeprecated(clusters.EggDeprecationWarning, "library", "egg")

		c.SetRequired("docker_image", "url")
		c.SetRequired("docker_image", "basic_auth", "password")
		c.SetSensitive("docker_image", "basic_auth", "password")
		c.SetRequired("docker_image", "basic_auth", "username")

		c.SetOptional("autoscale", "max_workers")
		c.SetOptional("autoscale", "min_workers")

		c.SetRequired("cluster_log_conf", "dbfs", "destination")
		c.SetRequired("cluster_log_conf", "s3", "destination")
		c.SetRequired("cluster_log_conf", "volumes", "destination")

		return c
	})

	attrs["idempotency_token"] = schema.StringAttribute{
		Optional: true,
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.RequiresReplace(),
		},
	}

	attrs["is_pinned"] = schema.BoolAttribute{
		Optional: true,
		Computed: true,
	}

	attrs["no_wait"] = schema.BoolAttribute{
		Optional: true,
		Computed: true,
	}

	attrs["autotermination_minutes"] = schema.Int64Attribute{
		Optional: true,
		Computed: true,
	}

	resp.Schema = schema.Schema{
		Description: "Terraform schema for Databricks Cluster",
		Attributes:  attrs,
		Blocks:      blocks,
	}
}

func (r *ClusterResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if r.Client == nil && req.ProviderData != nil {
		r.Client = pluginfwcommon.ConfigureResource(req, resp)
	}
}

func (r *ClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *ClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var clusterTfSDK ClusterSpecExtended
	resp.Diagnostics.Append(req.Plan.Get(ctx, &clusterTfSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, clusterTfSDK.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var createClusterGoSDK compute.CreateCluster
	resp.Diagnostics.Append(converters.TfSdkToGoSdkStruct(ctx, clusterTfSDK, &createClusterGoSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := clusters.ModifyRequestOnInstancePool(&createClusterGoSDK); err != nil {
		resp.Diagnostics.AddError("failed to modify request for instance pool", err.Error())
		return
	}
	clusters.SetForceSendFieldsForCluster(&createClusterGoSDK, nil)

	clusterWaiter, err := w.Clusters.Create(ctx, createClusterGoSDK)
	if err != nil {
		resp.Diagnostics.AddError("failed to create cluster", err.Error())
		return
	}

	clusterId := clusterWaiter.ClusterId
	clusterTfSDK.ID = types.StringValue(clusterId)
	clusterTfSDK.ClusterId = types.StringValue(clusterId)

	if clusterTfSDK.IsPinned.ValueBool() {
		if err := w.Clusters.PinByClusterId(ctx, clusterId); err != nil {
			resp.Diagnostics.AddError("failed to pin cluster", err.Error())
			return
		}
	}

	var libraryList []compute.Library
	if !clusterTfSDK.Library.IsNull() && !clusterTfSDK.Library.IsUnknown() {
		var libraryTfSDK []compute_tf.Library_SdkV2
		resp.Diagnostics.Append(clusterTfSDK.Library.ElementsAs(ctx, &libraryTfSDK, true)...)
		if resp.Diagnostics.HasError() {
			return
		}
		for _, lib := range libraryTfSDK {
			var libGoSDK compute.Library
			resp.Diagnostics.Append(converters.TfSdkToGoSdkStruct(ctx, lib, &libGoSDK)...)
			if resp.Diagnostics.HasError() {
				return
			}
			libraryList = append(libraryList, libGoSDK)
		}
		if len(libraryList) > 0 {
			if err := w.Libraries.Install(ctx, compute.InstallLibraries{
				ClusterId: clusterId,
				Libraries: libraryList,
			}); err != nil {
				resp.Diagnostics.AddError("failed to install libraries", err.Error())
				return
			}
		}
	}

	if clusterTfSDK.NoWait.ValueBool() {
		clusterTfSDK.Url = types.StringValue(r.Client.FormatURL("#setting/clusters/", clusterId, "/configuration"))
		resp.Diagnostics.Append(resp.State.Set(ctx, clusterTfSDK)...)
		return
	}

	clusterInfo, err := clusterWaiter.GetWithTimeout(clusterDefaultProvisionTimeout)
	if err != nil {
		resp.Diagnostics.Append(r.deleteCluster(ctx, w, clusterId)...)
		resp.Diagnostics.AddError("failed to wait for cluster creation", err.Error())
		return
	}

	if len(libraryList) > 0 {
		_, err := libraries.WaitForLibrariesInstalledSdk(ctx, w, compute.Wait{
			ClusterID: clusterId,
			IsRunning: clusterInfo.IsRunningOrResizing(),
			IsRefresh: false,
		}, clusterDefaultProvisionTimeout)
		if err != nil {
			resp.Diagnostics.AddError("failed to wait for library installation", err.Error())
			return
		}
	}

	newClusterTfSDK, diags := r.readClusterInfo(ctx, w, clusterId, &clusterTfSDK)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if newClusterTfSDK != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, newClusterTfSDK)...)
	}
}

func (r *ClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var clusterTfSDK ClusterSpecExtended
	resp.Diagnostics.Append(req.State.Get(ctx, &clusterTfSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, clusterTfSDK.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterId := clusterTfSDK.ID.ValueString()
	if clusterId == "" {
		clusterId = clusterTfSDK.ClusterId.ValueString()
	}

	newClusterTfSDK, diags := r.readClusterInfo(ctx, w, clusterId, &clusterTfSDK)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if newClusterTfSDK == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newClusterTfSDK)...)
}

func (r *ClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var planTfSDK ClusterSpecExtended
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planTfSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var stateTfSDK ClusterSpecExtended
	resp.Diagnostics.Append(req.State.Get(ctx, &stateTfSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, planTfSDK.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterId := stateTfSDK.ID.ValueString()
	if clusterId == "" {
		clusterId = stateTfSDK.ClusterId.ValueString()
	}

	var editClusterGoSDK compute.EditCluster
	resp.Diagnostics.Append(converters.TfSdkToGoSdkStruct(ctx, planTfSDK, &editClusterGoSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}
	editClusterGoSDK.ClusterId = clusterId

	if err := clusters.ModifyRequestOnInstancePool(&editClusterGoSDK); err != nil {
		resp.Diagnostics.AddError("failed to modify request for instance pool", err.Error())
		return
	}
	clusters.SetForceSendFieldsForCluster(&editClusterGoSDK, nil)

	err := retry.RetryContext(ctx, 15*time.Minute, func() *retry.RetryError {
		_, err := w.Clusters.Edit(ctx, editClusterGoSDK)
		if err == nil {
			return nil
		}
		var apiErr *apierr.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode == "INVALID_STATE" {
			return retry.RetryableError(fmt.Errorf("cluster %s cannot be modified in its current state", clusterId))
		}
		return retry.NonRetryableError(err)
	})
	if err != nil {
		resp.Diagnostics.AddError("failed to update cluster", err.Error())
		return
	}

	oldPinned := stateTfSDK.IsPinned.ValueBool()
	newPinned := planTfSDK.IsPinned.ValueBool()
	if oldPinned != newPinned {
		log.Printf("[DEBUG] Update: is_pinned. Old: %v, New: %v", oldPinned, newPinned)
		if newPinned {
			err = w.Clusters.PinByClusterId(ctx, clusterId)
		} else {
			err = w.Clusters.UnpinByClusterId(ctx, clusterId)
		}
		if err != nil {
			resp.Diagnostics.AddError("failed to update cluster pin status", err.Error())
			return
		}
	}

	resp.Diagnostics.Append(r.updateLibraries(ctx, w, clusterId, &planTfSDK, &stateTfSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	planTfSDK.ID = types.StringValue(clusterId)
	planTfSDK.ClusterId = types.StringValue(clusterId)
	newClusterTfSDK, diags := r.readClusterInfo(ctx, w, clusterId, &planTfSDK)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if newClusterTfSDK != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, newClusterTfSDK)...)
	}
}

func (r *ClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var clusterTfSDK ClusterSpecExtended
	resp.Diagnostics.Append(req.State.Get(ctx, &clusterTfSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, clusterTfSDK.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterId := clusterTfSDK.ID.ValueString()
	if clusterId == "" {
		clusterId = clusterTfSDK.ClusterId.ValueString()
	}

	resp.Diagnostics.Append(r.deleteCluster(ctx, w, clusterId)...)
}

func (r *ClusterResource) deleteCluster(ctx context.Context, w *databricks.WorkspaceClient, clusterId string) diag.Diagnostics {
	var diags diag.Diagnostics
	err := w.Clusters.PermanentDeleteByClusterId(ctx, clusterId)
	if err == nil || apierr.IsMissing(err) {
		return diags
	}
	if !strings.Contains(err.Error(), "unpin the cluster first") {
		diags.AddError("failed to delete cluster", err.Error())
		return diags
	}
	err = w.Clusters.UnpinByClusterId(ctx, clusterId)
	if err != nil {
		diags.AddError("failed to unpin cluster before deletion", err.Error())
		return diags
	}
	err = w.Clusters.PermanentDeleteByClusterId(ctx, clusterId)
	if err != nil && !apierr.IsMissing(err) {
		diags.AddError("failed to delete cluster after unpinning", err.Error())
	}
	return diags
}

func (r *ClusterResource) readClusterInfo(ctx context.Context, w *databricks.WorkspaceClient, clusterId string, clusterTfSDK *ClusterSpecExtended) (*ClusterSpecExtended, diag.Diagnostics) {
	var diags diag.Diagnostics

	clusterInfo, err := w.Clusters.GetByClusterId(ctx, clusterId)
	if err != nil {
		if apierr.IsMissing(err) {
			return nil, diags
		}
		diags.AddError("failed to get cluster", err.Error())
		return nil, diags
	}

	var newClusterTfSDK ClusterSpecExtended
	diags.Append(converters.GoSdkToTfSdkStruct(ctx, clusterInfo, &newClusterTfSDK)...)
	if diags.HasError() {
		return nil, diags
	}

	newClusterTfSDK.ID = types.StringValue(clusterId)
	newClusterTfSDK.ClusterId = types.StringValue(clusterId)
	newClusterTfSDK.State = types.StringValue(string(clusterInfo.State))
	newClusterTfSDK.Url = types.StringValue(r.Client.FormatURL("#setting/clusters/", clusterId, "/configuration"))

	diags.Append(r.setPinnedStatus(ctx, w, clusterId, &newClusterTfSDK)...)
	if diags.HasError() {
		return nil, diags
	}

	newClusterTfSDK.IsPinned = clusterTfSDK.IsPinned
	newClusterTfSDK.NoWait = clusterTfSDK.NoWait
	newClusterTfSDK.IdempotencyToken = clusterTfSDK.IdempotencyToken
	newClusterTfSDK.ProviderConfig = clusterTfSDK.ProviderConfig
	newClusterTfSDK.Library = clusterTfSDK.Library
	newClusterTfSDK.ClusterMountInfo = clusterTfSDK.ClusterMountInfo

	return &newClusterTfSDK, diags
}

func (r *ClusterResource) setPinnedStatus(ctx context.Context, w *databricks.WorkspaceClient, clusterId string, clusterTfSDK *ClusterSpecExtended) diag.Diagnostics {
	var diags diag.Diagnostics
	clusterDetails := w.Clusters.List(ctx, compute.ListClustersRequest{
		FilterBy: &compute.ListClustersFilterBy{
			IsPinned: true,
		},
		PageSize: 100,
	})

	for clusterDetails.HasNext(ctx) {
		detail, err := clusterDetails.Next(ctx)
		if err != nil {
			diags.AddError("failed to list pinned clusters", err.Error())
			return diags
		}
		if detail.ClusterId == clusterId {
			clusterTfSDK.IsPinned = types.BoolValue(true)
			return diags
		}
	}
	clusterTfSDK.IsPinned = types.BoolValue(false)
	return diags
}

func (r *ClusterResource) updateLibraries(ctx context.Context, w *databricks.WorkspaceClient, clusterId string, planTfSDK, stateTfSDK *ClusterSpecExtended) diag.Diagnostics {
	var diags diag.Diagnostics

	var planLibraries []compute.Library
	if !planTfSDK.Library.IsNull() && !planTfSDK.Library.IsUnknown() {
		var libraryTfSDK []compute_tf.Library_SdkV2
		diags.Append(planTfSDK.Library.ElementsAs(ctx, &libraryTfSDK, true)...)
		if diags.HasError() {
			return diags
		}
		for _, lib := range libraryTfSDK {
			var libGoSDK compute.Library
			diags.Append(converters.TfSdkToGoSdkStruct(ctx, lib, &libGoSDK)...)
			if diags.HasError() {
				return diags
			}
			planLibraries = append(planLibraries, libGoSDK)
		}
	}

	libsClusterStatus, err := w.Libraries.ClusterStatusByClusterId(ctx, clusterId)
	if err != nil {
		diags.AddError("failed to get library status", err.Error())
		return diags
	}

	libsToInstall, libsToUninstall := libraries.GetLibrariesToInstallAndUninstall(planLibraries, libsClusterStatus)

	if len(libsToUninstall) > 0 || len(libsToInstall) > 0 {
		clusterInfo, err := w.Clusters.GetByClusterId(ctx, clusterId)
		if err != nil {
			diags.AddError("failed to get cluster info", err.Error())
			return diags
		}
		if !clusterInfo.IsRunningOrResizing() {
			if _, err = w.Clusters.StartByClusterIdAndWait(ctx, clusterId); err != nil {
				diags.AddError("failed to start cluster for library update", err.Error())
				return diags
			}
		}
		err = w.Libraries.UpdateAndWait(ctx, compute.Update{
			ClusterId: clusterId,
			Install:   libsToInstall,
			Uninstall: libsToUninstall,
		})
		if err != nil {
			diags.AddError("failed to update libraries", err.Error())
			return diags
		}
		if clusterInfo.State == compute.StateTerminated {
			log.Printf("[INFO] %s was in TERMINATED state, so terminating it again", clusterId)
			if err = w.Clusters.DeleteByClusterId(ctx, clusterId); err != nil {
				diags.AddError("failed to terminate cluster after library update", err.Error())
				return diags
			}
		}
	}
	return diags
}
