# AWS Kill Switch

AWS Kill Switch is a Lambda function (and proof of concept client) that an organization can implement in a dedicated "Security" account to give their security engineers the ability to quickly deploy restrictions during a security incident, including:

* Apply a service control policy (SCP) to freeze the state of a targeted account
* Detach all policies and delete inline policies from a targeted IAM role
* Revoke all sessions on a targeted IAM role or `ALL` customer managed IAM roles in a targeted account
* Delete a targeted IAM role (which also revokes all sessions)
 
## Prerequisites

- [Go](https://golang.org/dl/)

Tested on go1.21.3 on arm64. 

## Preparation

This tool requires you to have roles that you can assume from a dedicated "Security" account to your organization management account (`apply_scp`) or to any account in your organization (actions other than `apply_scp`). You can use [AWS CloudFormation StackSets](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/what-is-cfnstacksets.html) to automate the creation of these roles.

### Required permissions

| Action | Required permissions | Other requirements
| --- | --- | --- |
| `apply_scp` | organizations:CreatePolicy, organizations:AttachPolicy | Role to assume must be in the organization management account
| `detach_policies` | iam:ListAttachedRolePolicies, iam:DetachRolePolicy, iam:ListRolePolicies, iam:DeleteRolePolicy | Role to assume must be in the targeted account
| `revoke_sessions` | iam:AttachRolePolicy, iam:CreatePolicy, iam:ListRoles | Role to assume must be in the targeted account
| `delete_role` | iam:DeleteRole, iam:ListAttachedRolePolicies, iam:DetachRolePolicy, iam:ListRolePolicies, iam:DeleteRolePolicy | Role to assume must be in the targeted account

### Prevent tampering

You should take steps to ensure that a threat actor cannot make modifications to the IAM role that you plan to assume during a security incident. Consider implementing a SCP like:

```
{    
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "DenyAccessToASpecificRole",
      "Effect": "Deny",
      "Action": [
        "iam:AttachRolePolicy",
        "iam:DeleteRole",
        "iam:DeleteRolePermissionsBoundary",
        "iam:DeleteRolePolicy",
        "iam:DetachRolePolicy",
        "iam:PutRolePermissionsBoundary",
        "iam:PutRolePolicy",
        "iam:UpdateAssumeRolePolicy",
        "iam:UpdateRole",
        "iam:UpdateRoleDescription"
      ],
      "Resource": [
        "arn:aws:iam::*:role/security-role"
      ]
    }
  ]
}
```

This example assumes that you created a service managed StackSet in your organization that automatically creates `security-role` in every account. With this SCP the threat actor will be unable to tamper with your role or attached policies, even if they have elevated permissions that would otherwise allow manipulation of roles and policies.

## Installation

### Clone the Repository

```
git clone https://github.com/secengjeff/awskillswitch.git
```

### Installing

:warning: Before creating the awskillswitch Lambda function review `awskillswitch.go` and consider modifying `switch.conf` to meet your organization's unique requirements. By default the `apply_scp` action will restrict most IAM actions on the account. This may break your application.

Follow these steps to build the awskillswitch Lambda function and zip the binary: 

```
cd awskillswitch

GOOS="linux" GOARCH="amd64" go build -o main awskillswitch.go

zip main.zip main switch.conf
```

Create an execution role (`execution_role.json`):

```
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Service": "lambda.amazonaws.com"
      },
      "Action": "sts:AssumeRole"
    }
  ]
}
```

Create a permission policy (`permission_policy.json`):

```
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": "sts:AssumeRole",
            "Resource": "arn:aws:iam::*:role/*"
        },
        {
            "Effect": "Allow",
            "Action": [
                "logs:CreateLogGroup",
                "logs:CreateLogStream",
                "logs:PutLogEvents"
            ],
            "Resource": "arn:aws:logs:*:*:*"
        }
    ]
}
```

Deploy the execution role, permission policy, and create the Lambda function:

```
aws iam create-role --role-name executionRole --assume-role-policy-document file://execution_role.json

aws iam put-role-policy --role-name executionRole --policy-name permissionPolicy --policy-document file://permission_policy.json

aws lambda create-function --function-name awskillswitch --zip-file fileb://main.zip --handler main --runtime go1.x --role executionRoleArn --timeout 15 --memory-size 128
```

You can test the function using the included proof of concept client. You can use this as a standalone application or as an example of how to invoke the client from within your own tooling and automation:

```
cd awskillswitch/client

go build -o killswitchclient killswitchclient.go
```

### Usage

:warning: The actions you take with this tool are one-way operations. Do not test/experiment in production. Any SCPs applied or IAM roles deleted will remain in this state until manual action is taken to remove the SCP or recreate deleted role and/or policies. Ensure that you have the the ability to reverse these changes and incorporate the appropriate steps in your incident response playbooks. 

### Environment

You can run this client locally by manually setting AWS CLI environment variables `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_SESSION_TOKEN` (if applicable) for any IAM user or assumed role with a policy that allows `lambda:InvokeFunction` for the ARN of the function that you created. It will not function if you're assuming a role using the `AWS_PROFILE` variable. You can also run this client from an EC2 instance with an instance policy that allows `lambda:InvokeFunction` for the ARN of the function that you created.

### Flags

- `action`: Specifies the action to perform. Valid values are `apply_scp`, `detach_policies`, or `delete_role`.
- `lambda`: The name or ARN of the AWS Lambda function to invoke.
- `target_account`: The AWS Account ID where the action will take place.
- `role_to_assume`: The IAM role that will be assumed by the Lambda function to perform the action.
- `target_role`: The name of the IAM role to delete (required only for the `detach_policies` and `delete_role` actions). You may specify `ALL` when used with the `revoke_sessions` action to apply the action to all customer managed IAM roles in the targeted account.
- `org_management_account`: The AWS Organization's management account ID (required only for the `apply_scp` action).
- `region`: The AWS region where the Lambda function is deployed (defaults to us-east-1 if none provided)

If you prefer to call the Lambda function directly your application will need to `invokeLambda` with one of the following payloads:

**To apply a restrictive SCP:**

```
{
  "action": "apply_scp",
  "target_account_id": "123456789012",
  "role_to_assume": "RoleToAssume",
  "org_management_account": "998877665544"
}

```

This call will apply the SCP defined by `scpPolicy` in `switch.conf` to the AWS account `123456789012` by assuming `RoleToAssume` in AWS account `998877665544`.

**To detach policies from an IAM role:**

```
{
  "action": "detach_policies",
  "target_account_id": "210987654321",
  "role_to_assume": "RoleToAssume",
  "target_role_name": "RoleToDetach"
}

```

This call will detach IAM policies and delete inline IAM policies from `RoleToDetach` in AWS account `210987654321` by assuming `RoleToAssume` in the same account. Currently active session tokens **will remain valid** with all relevant permissions until expired.

**To revoke sessions:**

```
{
  "action": "revoke_sessions",
  "target_account_id": "210987654321",
  "role_to_assume": "RoleToAssume",
  "target_role_name": "RoleToRevokeSessions"
}

```

This call will apply a new policy that invalidates session tokens issued prior to the current time for `RoleToRevokeSessions` in AWS account `210987654321` by assuming `RoleToAssume` in the same account.

```
{
  "action": "revoke_sessions",
  "target_account_id": "210987654321",
  "role_to_assume": "RoleToAssume",
  "target_role_name": "ALL"
}

```

This call will apply a new policy that invalidates session tokens issued prior to the current time for all customer managed IAM roles in AWS account `210987654321` by assuming `RoleToAssume` in the same account.

**To delete an IAM role:**

```
{
  "action": "delete_role",
  "target_account_id": "210987654321",
  "role_to_assume": "RoleToAssume",
  "target_role_name": "RoleToDelete"
}

```

This call will delete the IAM role `RoleToDelete` in AWS account `210987654321` by assuming `RoleToAssume` in the same account. Deleting a role will invalidate all session tokens for that role.

### Example

**Applying a restrictive SCP**

```
./awskillswitch -action apply_scp -lambda "LambdaArn" -target_account "123456789012" -role_to_assume "RoleToAssume" -org_management_account "998877665544" -region "us-east-1"
```

This command will apply the SCP defined by `scpPolicy` in `switch.conf` to the AWS account `123456789012` by assuming `RoleToAssume` in AWS account `998877665544` using the `LambdaArn` function deployed to `us-east-1`.

**Detaching policies from an IAM role:**

```
./awskillswitch -action detach_policies -lambda "LambdaArn" -target_account "210987654321" -role_to_assume "RoleToAssume" -target_role "RoleToDelete" -region "us-east-1"
```

This command will detach IAM policies and delete inline policies from `RoleToDetach` in AWS account `210987654321` by assuming `RoleToAssume` in the same account using the `LambdaArn` Lambda function deployed to `us-east-1`. Currently active session tokens **will remain valid** with all relevant permissions until expired.

**Deleting an IAM role:**

```
./awskillswitch -action delete_role -lambda "LambdaArn" -target_account "210987654321" -role_to_assume "RoleToAssume" -target_role "RoleToDelete" -region "us-east-1"
```

This command will delete the IAM role `RoleToDelete` in AWS account `210987654321` by assuming `RoleToAssume` in the same account using the `LambdaArn` Lambda function deployed to `us-east-1`. Deleting a role will invalidate all session tokens for that role.

**Revoking sessions:**

```
./awskillswitch -action revoke_sessions -lambda "LambdaArn" -target_account "210987654321" -role_to_assume "RoleToAssume" -target_role "RoleToRevokeSessions" -region "us-east-1"
```

This command will apply a new policy that invalidates session tokens issued prior to the current time for `RoleToRevokeSessions` in AWS account `210987654321` by assuming `RoleToAssume` in the same account using the `LambdaArn` Lambda function deployed to `us-east-1`. 

```
./awskillswitch -action revoke_sessions -lambda "LambdaArn" -target_account "210987654321" -role_to_assume "RoleToAssume" -target_role "ALL" -region "us-east-1"
```

This command will apply a new policy that invalidates session tokens issued prior to the current time for all customer managed IAM roles in AWS account `210987654321` by assuming `RoleToAssume` in the same account using the `LambdaArn` Lambda function deployed to `us-east-1`. 

## Built With

- [aws-sdk-go](https://pkg.go.dev/github.com/aws/aws-sdk-go) - AWS SDK

- [aws-lambda-go](https://pkg.go.dev/github.com/aws/aws-lambda-go) - AWS Lambda libraries

## Authors

-  Jeffrey Lyon  -  *Initial release*  - @secengjeff

See also the list of [contributors](https://github.com/secengjeff/rapidresetclient/contributors)

## License

This project is licensed under the Apache License - see the [LICENSE](LICENSE) file for details