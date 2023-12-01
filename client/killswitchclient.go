package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
)

type ActionType string

const (
	ApplySCP       ActionType = "apply_scp"
	DeleteRole     ActionType = "delete_role"
	DetachPolicies ActionType = "detach_policies"
	RevokeSessions ActionType = "revoke_sessions"
)

// LambdaRequest defines the payload structure to send to the Lambda function
type LambdaRequest struct {
	Action                 ActionType `json:"action"`
	TargetAccountID        string     `json:"target_account_id"`
	RoleToAssume           string     `json:"role_to_assume"`
	TargetRoleName         string     `json:"target_role_name,omitempty"`       // Role name for actions other than apply_scp
	OrgManagementAccountID string     `json:"org_management_account,omitempty"` // Management account ID for apply_scp
	Region                 string     `json:"region,omitempty"`
}

// invokeLambda calls a Lambda function with the provided payload
func invokeLambda(functionName string, payload []byte, region string) (*lambda.InvokeOutput, error) {
	var sess *session.Session
	if region == "" {
		sess = session.Must(session.NewSession())
	} else {
		sess = session.Must(session.NewSession(&aws.Config{
			Region: aws.String(region),
		}))
	}
	lambdaSvc := lambda.New(sess)

	input := &lambda.InvokeInput{
		FunctionName:   aws.String(functionName),
		Payload:        payload,
		InvocationType: aws.String("RequestResponse"),
	}

	return lambdaSvc.Invoke(input)
}

func main() {
	// Parse command-line flags
	var (
		actionFlag             string
		lambdaFlag             string
		targetAccountFlag      string
		roleToAssumeFlag       string
		targetRoleFlag         string
		orgManagementAccountID string
		regionFlag             string
	)
	flag.StringVar(&actionFlag, "action", "", "Action to perform: 'apply_scp', 'delete_role', 'detach_policies', or 'revoke_sessions")
	flag.StringVar(&lambdaFlag, "lambda", "", "Lambda function name or ARN")
	flag.StringVar(&targetAccountFlag, "target_account", "", "AWS target account ID to perform the action on")
	flag.StringVar(&roleToAssumeFlag, "role_to_assume", "", "Role to assume when performing the action")
	flag.StringVar(&targetRoleFlag, "target_role", "", "IAM role name to delete, detach, or revoke sessions on. Required for actions other than 'apply_scp (for delete_role, detach_policies, or revoke_sessions only). Specify ALL to revoke all sessions on all roles when using the revoke_sessions action.")
	flag.StringVar(&orgManagementAccountID, "org_management_account", "", "AWS Org Management Account ID (for apply_scp only)")
	flag.StringVar(&regionFlag, "region", "", "AWS region of the Lambda function")
	flag.Parse()

	// Validate flags
	if actionFlag == "" || lambdaFlag == "" || targetAccountFlag == "" || roleToAssumeFlag == "" {
		fmt.Println("Required flags not provided. 'action', 'lambda', 'target_account', and 'role_to_assume' are mandatory.")
		os.Exit(1)
	}
	if actionFlag == string(ApplySCP) && orgManagementAccountID == "" {
		fmt.Println("For 'apply_scp' action, 'org_management_account' flag is also required.")
		os.Exit(1)
	}
	if (actionFlag == string(DeleteRole) || actionFlag == string(DetachPolicies)) || actionFlag == string(RevokeSessions) && targetRoleFlag == "" {
		fmt.Println("The 'target_role' flag is required for actions other than 'apply_scp'.")
		os.Exit(1)
	}

	// Build the request payload based on the action
	request := LambdaRequest{
		Action:                 ActionType(actionFlag),
		TargetAccountID:        targetAccountFlag,
		RoleToAssume:           roleToAssumeFlag,
		TargetRoleName:         targetRoleFlag,
		OrgManagementAccountID: orgManagementAccountID,
		Region:                 regionFlag,
	}

	// Marshal the request into JSON
	payload, err := json.Marshal(request)
	if err != nil {
		fmt.Printf("Error marshalling the lambda payload: %s\n", err)
		os.Exit(1)
	}

	// Invoke the Lambda function with the payload and region
	result, err := invokeLambda(lambdaFlag, payload, regionFlag)
	if err != nil {
		fmt.Printf("Error invoking Lambda function: %s\n", err)
		os.Exit(1)
	}

	// Print the result of the Lambda invocation
	var resultString string
	err = json.Unmarshal(result.Payload, &resultString)
	if err != nil {
		fmt.Printf("Error unmarshalling Lambda result: %s\n", err)
		os.Exit(1)
	}

	fmt.Println("Lambda invocation result:")
	fmt.Println(resultString)
}
