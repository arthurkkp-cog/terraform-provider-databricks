package jobs

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/apierr"
	"github.com/databricks/databricks-sdk-go/service/jobs"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/converters"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/databricks/terraform-provider-databricks/internal/service/jobs_tf"
	"github.com/databricks/terraform-provider-databricks/repos"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const resourceName = "job"

var _ resource.ResourceWithConfigure = &JobResource{}

func ResourceJob() resource.Resource {
	return &JobResource{}
}

type JobSettingsExtended struct {
	jobs_tf.JobSettings_SdkV2
	tfschema.Namespace_SdkV2
	ID  types.String `tfsdk:"id"`
	URL types.String `tfsdk:"url"`
}

var _ pluginfwcommon.ComplexFieldTypeProvider = JobSettingsExtended{}

func (j JobSettingsExtended) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	attrs := j.JobSettings_SdkV2.GetComplexFieldTypes(ctx)
	attrs["provider_config"] = reflect.TypeOf(tfschema.ProviderConfig{})
	return attrs
}

type JobResource struct {
	Client *common.DatabricksClient
}

func (r *JobResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(resourceName)
}

func (r *JobResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs, blocks := tfschema.ResourceStructToSchemaMap(ctx, JobSettingsExtended{}, func(c tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		c.ConfigureAsSdkV2Compatible()

		c.SetOptional("id")
		c.SetComputed("id")

		c.SetOptional("url")
		c.SetComputed("url")

		c.SetComputed("run_as")
		c.SetComputed("task", "retry_on_timeout")
		c.SetComputed("format")

		c.AddValidator(listvalidator.SizeAtMost(1), "provider_config")

		return c
	})
	resp.Schema = schema.Schema{
		Description: "Terraform schema for Databricks Job",
		Attributes:  attrs,
		Blocks:      blocks,
	}
}

func (r *JobResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if r.Client == nil && req.ProviderData != nil {
		r.Client = pluginfwcommon.ConfigureResource(req, resp)
	}
}

func (r *JobResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func sortTasksByKey(tasks []jobs.Task) {
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].TaskKey < tasks[j].TaskKey
	})
	for _, task := range tasks {
		sort.Slice(task.DependsOn, func(i, j int) bool {
			return task.DependsOn[i].TaskKey < task.DependsOn[j].TaskKey
		})
		sortWebhookNotifications(task.WebhookNotifications)
	}
}

func sortWebhookNotifications(wn *jobs.WebhookNotifications) {
	if wn == nil {
		return
	}
	notifs := [][]jobs.Webhook{wn.OnStart, wn.OnFailure, wn.OnSuccess,
		wn.OnDurationWarningThresholdExceeded, wn.OnStreamingBacklogExceeded}
	for _, ns := range notifs {
		sort.Slice(ns, func(i, j int) bool {
			return ns[i].Id < ns[j].Id
		})
	}
}

func adjustJobSettings(settings *jobs.JobSettings) {
	if settings == nil {
		return
	}
	sortTasksByKey(settings.Tasks)
	sortWebhookNotifications(settings.WebhookNotifications)
}

func (r *JobResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var plan JobSettingsExtended
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, plan.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var createJobGoSDK jobs.CreateJob
	resp.Diagnostics.Append(converters.TfSdkToGoSdkStruct(ctx, plan, &createJobGoSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(prepareJobForCreate(ctx, &createJobGoSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobResp, err := w.Jobs.Create(ctx, createJobGoSDK)
	if err != nil {
		resp.Diagnostics.AddError("failed to create job", err.Error())
		return
	}

	jobID := jobResp.JobId
	plan.ID = types.StringValue(strconv.FormatInt(jobID, 10))
	plan.URL = types.StringValue(r.Client.FormatURL("#job/", strconv.FormatInt(jobID, 10)))

	resp.Diagnostics.Append(r.readJobIntoState(ctx, w, jobID, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *JobResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var state JobSettingsExtended
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, state.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobID, err := parseJobID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("failed to parse job ID", err.Error())
		return
	}

	resp.Diagnostics.Append(r.readJobIntoState(ctx, w, jobID, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *JobResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var plan JobSettingsExtended
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state JobSettingsExtended
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, plan.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobID, err := parseJobID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("failed to parse job ID", err.Error())
		return
	}

	var jobSettingsGoSDK jobs.JobSettings
	resp.Diagnostics.Append(converters.TfSdkToGoSdkStruct(ctx, plan, &jobSettingsGoSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(prepareJobSettingsForUpdate(ctx, &jobSettingsGoSDK)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err = w.Jobs.Reset(ctx, jobs.ResetJob{
		JobId:       jobID,
		NewSettings: jobSettingsGoSDK,
	})
	if err != nil {
		resp.Diagnostics.AddError("failed to update job", err.Error())
		return
	}

	plan.ID = state.ID
	plan.URL = types.StringValue(r.Client.FormatURL("#job/", strconv.FormatInt(jobID, 10)))

	resp.Diagnostics.Append(r.readJobIntoState(ctx, w, jobID, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *JobResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	ctx = pluginfwcontext.SetUserAgentInResourceContext(ctx, resourceName)

	var state JobSettingsExtended
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, state.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := r.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobID, err := parseJobID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("failed to parse job ID", err.Error())
		return
	}

	err = w.Jobs.DeleteByJobId(ctx, jobID)
	if err != nil && !apierr.IsMissing(err) {
		resp.Diagnostics.AddError("failed to delete job", err.Error())
	}
}

func (r *JobResource) readJobIntoState(ctx context.Context, w *databricks.WorkspaceClient, jobID int64, state *JobSettingsExtended) diag.Diagnostics {
	var d diag.Diagnostics

	job, err := w.Jobs.Get(ctx, jobs.GetJobRequest{
		JobId: jobID,
	})
	if err != nil {
		if apierr.IsMissing(err) {
			d.AddWarning("job not found", fmt.Sprintf("job %d not found, marking as deleted", jobID))
			return d
		}
		d.AddError("failed to read job", err.Error())
		return d
	}

	if job.Settings != nil {
		adjustJobSettings(job.Settings)

		if job.RunAsUserName != "" {
			if common.StringIsUUID(job.RunAsUserName) {
				job.Settings.RunAs = &jobs.JobRunAs{
					ServicePrincipalName: job.RunAsUserName,
				}
			} else {
				job.Settings.RunAs = &jobs.JobRunAs{
					UserName: job.RunAsUserName,
				}
			}
		}
	}

	var newState JobSettingsExtended
	d.Append(converters.GoSdkToTfSdkStruct(ctx, job.Settings, &newState)...)
	if d.HasError() {
		return d
	}

	newState.ID = types.StringValue(strconv.FormatInt(jobID, 10))
	newState.URL = types.StringValue(r.Client.FormatURL("#job/", strconv.FormatInt(jobID, 10)))
	newState.ProviderConfig = state.ProviderConfig

	*state = newState
	return d
}

func parseJobID(id string) (int64, error) {
	return strconv.ParseInt(id, 10, 64)
}

func prepareJobForCreate(ctx context.Context, createJob *jobs.CreateJob) diag.Diagnostics {
	var d diag.Diagnostics

	sortTasksByKey(createJob.Tasks)
	sortWebhookNotifications(createJob.WebhookNotifications)

	if createJob.GitSource != nil && createJob.GitSource.GitProvider == "" {
		provider := jobs.GitProvider(repos.GetGitProviderFromUrl(createJob.GitSource.GitUrl))
		createJob.GitSource.GitProvider = provider
		if createJob.GitSource.GitProvider == "" {
			d.AddError("invalid git source", fmt.Sprintf("git source is not empty but Git Provider is not specified and cannot be guessed by url %+v", createJob.GitSource))
			return d
		}
		if createJob.GitSource.GitBranch == "" && createJob.GitSource.GitTag == "" && createJob.GitSource.GitCommit == "" {
			d.AddError("invalid git source", "git source is not empty but none of branch, commit and tag is specified")
			return d
		}
	}

	if createJob.Queue == nil {
		createJob.Queue = &jobs.QueueSettings{
			Enabled: false,
		}
	}

	return d
}

func prepareJobSettingsForUpdate(ctx context.Context, settings *jobs.JobSettings) diag.Diagnostics {
	var d diag.Diagnostics

	sortTasksByKey(settings.Tasks)
	sortWebhookNotifications(settings.WebhookNotifications)

	if settings.Queue == nil {
		settings.Queue = &jobs.QueueSettings{
			Enabled: false,
		}
	}

	return d
}
