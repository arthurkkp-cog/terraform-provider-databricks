package jobs

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/databricks/databricks-sdk-go/service/jobs"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceName = "jobs"

const (
	JobsGroupByName = "name"
	JobsGroupByID   = "id"
)

func DataSourceJobs() datasource.DataSource {
	return &JobsDataSource{}
}

var _ datasource.DataSourceWithConfigure = &JobsDataSource{}

type JobsDataSource struct {
	Client *common.DatabricksClient
}

type JobsData struct {
	Ids        types.Map    `tfsdk:"ids"`
	NameFilter types.String `tfsdk:"job_name_contains"`
	Key        types.String `tfsdk:"key"`
	tfschema.Namespace
}

func (JobsData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["ids"] = attrs["ids"].SetOptional().SetComputed()
	attrs["job_name_contains"] = attrs["job_name_contains"].SetOptional()
	attrs["key"] = attrs["key"].SetOptional()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (JobsData) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"ids":             reflect.TypeOf(types.String{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfigData{}),
	}
}

func (d *JobsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceName)
}

func (d *JobsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, JobsData{}, nil)
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *JobsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *JobsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceName)

	var data JobsData
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceIDDataSource(ctx, data.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	iter := w.Jobs.List(ctx, jobs.ListJobsRequest{ExpandTasks: false, Limit: 100})
	ids := map[string]string{}
	nameFilter := strings.ToLower(data.NameFilter.ValueString())

	keyAttribute := data.Key.ValueString()
	if keyAttribute == "" {
		keyAttribute = JobsGroupByName
	}
	keyAttribute = strings.ToLower(keyAttribute)

	for iter.HasNext(ctx) {
		job, err := iter.Next(ctx)
		if err != nil {
			resp.Diagnostics.AddError("failed to list jobs", err.Error())
			return
		}
		name := job.Settings.Name
		if nameFilter != "" && !strings.Contains(strings.ToLower(name), nameFilter) {
			continue
		}
		jobId := strconv.FormatInt(job.JobId, 10)

		key := name
		if strings.EqualFold(keyAttribute, JobsGroupByName) {
			key = name
		} else if strings.EqualFold(keyAttribute, JobsGroupByID) {
			key = jobId
		} else {
			resp.Diagnostics.AddError("invalid key attribute", fmt.Sprintf("unsupported key %s, must be one of %s or %s", keyAttribute, JobsGroupByName, JobsGroupByID))
			return
		}

		if _, duplicateKey := ids[key]; duplicateKey {
			resp.Diagnostics.AddError("duplicate job detected", fmt.Sprintf("duplicate job %s detected: %s", keyAttribute, key))
			return
		}
		ids[key] = jobId
	}

	idsMap, diags := types.MapValueFrom(ctx, types.StringType, ids)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Ids = idsMap

	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
