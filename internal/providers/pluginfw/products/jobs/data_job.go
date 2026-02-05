package jobs

import (
	"context"
	"fmt"
	"reflect"
	"strconv"

	"github.com/databricks/databricks-sdk-go/service/jobs"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/converters"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/databricks/terraform-provider-databricks/internal/service/jobs_tf"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceName = "job"

func DataSourceJob() datasource.DataSource {
	return &JobDataSource{}
}

var _ datasource.DataSourceWithConfigure = &JobDataSource{}

type JobDataSource struct {
	Client *common.DatabricksClient
}

type JobData struct {
	Id          types.String `tfsdk:"id"`
	JobId       types.String `tfsdk:"job_id"`
	Name        types.String `tfsdk:"name"`
	JobName     types.String `tfsdk:"job_name"`
	JobSettings types.List   `tfsdk:"job_settings"`
	tfschema.Namespace
}

func (JobData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["id"] = attrs["id"].SetOptional().SetComputed()
	attrs["job_id"] = attrs["job_id"].SetOptional().SetComputed()
	attrs["name"] = attrs["name"].SetOptional().SetComputed()
	attrs["job_name"] = attrs["job_name"].SetOptional().SetComputed()
	attrs["job_settings"] = attrs["job_settings"].SetOptional().SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (JobData) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"job_settings":    reflect.TypeOf(jobs_tf.Job_SdkV2{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfigData{}),
	}
}

func (d *JobDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceName)
}

func (d *JobDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, JobData{}, func(cs tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		cs.ConfigureAsSdkV2Compatible()
		return cs
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *JobDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *JobDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceName)

	var jobData JobData
	resp.Diagnostics.Append(req.Config.Get(ctx, &jobData)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceIDDataSource(ctx, jobData.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := jobData.Id.ValueString()
	jobId := jobData.JobId.ValueString()
	name := jobData.Name.ValueString()
	jobName := jobData.JobName.ValueString()

	if id == "" {
		id = jobId
	}
	if name == "" {
		name = jobName
	}

	var job *jobs.Job
	var jobIdInt int64

	if name != "" {
		jobsList, err := w.Jobs.ListAll(ctx, jobs.ListJobsRequest{
			Name:        name,
			ExpandTasks: true,
		})
		if err != nil {
			resp.Diagnostics.AddError("failed to list jobs", err.Error())
			return
		}

		var foundJobId int64
		for _, j := range jobsList {
			currentJobId := fmt.Sprintf("%d", j.JobId)
			currentJobName := ""
			if j.Settings != nil {
				currentJobName = j.Settings.Name
			}
			if currentJobName == name || currentJobId == id {
				foundJobId = j.JobId
				name = currentJobName
				break
			}
		}

		if foundJobId == 0 {
			resp.Diagnostics.AddError("no job found with specified name", "")
			return
		}
		jobIdInt = foundJobId
	} else {
		var err error
		jobIdInt, err = strconv.ParseInt(id, 10, 64)
		if err != nil {
			resp.Diagnostics.AddError("invalid job_id", err.Error())
			return
		}
	}

	var err error
	job, err = w.Jobs.Get(ctx, jobs.GetJobRequest{
		JobId: jobIdInt,
	})
	if err != nil {
		resp.Diagnostics.AddError("failed to get job", err.Error())
		return
	}
	if job.Settings != nil {
		name = job.Settings.Name
	}
	id = fmt.Sprintf("%d", job.JobId)

	if job.Settings != nil && job.RunAsUserName != "" {
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

	var tfJob jobs_tf.Job_SdkV2
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, job, &tfJob)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobData.Id = types.StringValue(id)
	jobData.JobId = types.StringValue(id)
	jobData.Name = types.StringValue(name)
	jobData.JobName = types.StringValue(name)
	jobData.JobSettings = types.ListValueMust(tfJob.Type(ctx), []attr.Value{tfJob.ToObjectValue(ctx)})

	resp.Diagnostics.Append(resp.State.Set(ctx, jobData)...)
}
