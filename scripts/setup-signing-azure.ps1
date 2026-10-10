<#
.SYNOPSIS
AZURE HALF of wiring GitHub Actions to an existing Azure Artifact Signing account. Safe to re-run.

Split from the one-script version in SpoolSmith so it can run on a machine that has `az` signed in but no GitHub access
(and so no GitHub token ever has to be copied there). Run this where `az login` works, then run scripts/setup-signing-github.sh
wherever `gh` is signed in, feeding it the JSON this prints.

.DESCRIPTION
Does everything in docs/code-signing.md that can be scripted:

  1. Reads the signing account and certificate profile and refuses to continue
     unless the profile is Active.
  2. Creates an app registration and service principal for the release workflow.
  3. Adds a federated credential trusting exactly
     <GitHub's subject prefix for the repo>:environment:<Environment>, so no
     secret is ever stored.
  4. Grants that service principal "Artifact Signing Certificate Profile Signer"
     on the certificate profile only — not the account, not the subscription.
  5. Creates the GitHub environment and sets the signing variables and the three
     Azure identifiers as environment-scoped values, so only a job that declares
     that environment can read them.
  6. Restricts the environment to the default branch and version tags, so a
     branch anyone with write access can push cannot sign as this identity.

Each step checks before it creates, so a second run changes nothing.

It works for any repository, not just this one: every repo gets its own app
registration, named <repo>-release-signing, so access can be revoked per repo
and one repo's credential cannot sign for another.

What it deliberately does not do: create the signing account, or complete
identity validation. Validation is a document check done in the Azure portal
and can take days; the certificate profile cannot exist until it succeeds, which
is why this script requires the profile to exist already.

Prerequisites: `az login` as someone who can create app registrations and assign
roles on the subscription, and GitHub access to the repository as an admin, via
`gh auth login` or a GH_TOKEN with repo scope.

.EXAMPLE
./scripts/setup-signing.ps1 -AccountName jdspille -ResourceGroup RG0 -ProfileName primary-profile
#>
param(
    [Parameter(Mandatory)][string]$AccountName,
    [Parameter(Mandatory)][string]$ResourceGroup,
    [Parameter(Mandatory)][string]$ProfileName,
    [string]$Repo = 'spilloid/minute2win',
    [string]$Environment = 'release',
    # Defaults to <repo>-release-signing.
    [string]$AppName,
    # Substring the signer subject must contain; defaults to the certificate
    # profile's common name so a binary signed by another profile fails CI.
    [string]$ExpectedSubject,
    # Refs allowed to run jobs in the environment. A published release runs as
    # its tag and a manual run as the default branch, so both are needed.
    [string[]]$DeploymentBranches = @('main'),
    [string[]]$DeploymentTags = @('v*'),
    # Turn on GitHub's immutable OIDC subject claims for the repository first,
    # so the credential trusts numeric IDs and a renamed, deleted or re-created
    # repository cannot inherit it.
    # The OIDC subject prefix GitHub presents for this repo, from the GitHub side:
    #   gh api repos/<repo>/actions/oidc/customization/sub -q .sub_claim_prefix
    # (scripts/setup-signing-github.sh prints it). Required: this half never talks to GitHub.
    [Parameter(Mandatory)][string]$SubjectPrefix
)
$ErrorActionPreference = 'Stop'
if (-not $AppName) { $AppName = "$($Repo.Split('/')[-1].ToLowerInvariant())-release-signing" }
$signerRole = 'Artifact Signing Certificate Profile Signer'
$armApi = '2024-09-30-preview'

function Invoke-Native {
    # Runs a native command, throws on a non-zero exit, returns its output lines.
    param([Parameter(Mandatory)][string]$Command, [string[]]$Arguments)
    $output = & $Command @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) { throw "$Command $($Arguments -join ' ') failed:`n$($output -join "`n")" }
    return $output
}
function Invoke-AzJson { param([string[]]$Arguments) (Invoke-Native az ($Arguments + @('-o', 'json')) | Out-String) | ConvertFrom-Json }
function Step { param([string]$Text) Write-Host "`n== $Text" }

Step 'Azure context'
$account = Invoke-AzJson @('account', 'show')
$subscriptionId = $account.id
$tenantId = $account.tenantId
Write-Host "subscription $($account.name), tenant $tenantId, signed in as $($account.user.name)"

Step 'Signing account and certificate profile'
$accountId = "/subscriptions/$subscriptionId/resourceGroups/$ResourceGroup/providers/Microsoft.CodeSigning/codeSigningAccounts/$AccountName"
$signing = Invoke-AzJson @('rest', '--method', 'get', '--url', "https://management.azure.com${accountId}?api-version=$armApi")
$endpoint = $signing.properties.accountUri.TrimEnd('/')
$profileId = "$accountId/certificateProfiles/$ProfileName"
$signingProfile = Invoke-AzJson @('rest', '--method', 'get', '--url', "https://management.azure.com${profileId}?api-version=$armApi")
if ($signingProfile.properties.status -ne 'Active') {
    throw "Certificate profile '$ProfileName' is '$($signingProfile.properties.status)', not Active. Identity validation may still be pending."
}
Write-Host "endpoint $endpoint, profile $ProfileName ($($signingProfile.properties.profileType)), active"
if (-not $ExpectedSubject) {
    $subject = $signingProfile.properties.certificates | Where-Object status -eq 'Active' | Select-Object -First 1 -ExpandProperty subjectName
    if ($subject -match '(CN=[^,]+)') { $ExpectedSubject = $Matches[1] }
}
Write-Host "expected signer subject contains: $ExpectedSubject"

Step "App registration '$AppName'"
$apps = @(Invoke-AzJson @('ad', 'app', 'list', '--display-name', $AppName) | Where-Object displayName -eq $AppName)
if ($apps.Count -gt 1) { throw "$($apps.Count) app registrations are named '$AppName'; remove the duplicates so this script cannot pick the wrong one." }
if ($apps.Count -eq 1) { $app = $apps[0]; Write-Host 'already exists' }
else { $app = Invoke-AzJson @('ad', 'app', 'create', '--display-name', $AppName, '--sign-in-audience', 'AzureADMyOrg'); Write-Host 'created' }

Step 'Service principal'
$principals = @(Invoke-AzJson @('ad', 'sp', 'list', '--filter', "appId eq '$($app.appId)'"))
if ($principals.Count -ge 1) { $principal = $principals[0]; Write-Host 'already exists' }
else { $principal = Invoke-AzJson @('ad', 'sp', 'create', '--id', $app.appId); Write-Host 'created' }

Step "Federated credential for environment '$Environment'"
$subjectClaim = "${SubjectPrefix}:environment:$Environment"
$credentialName = "github-$Environment"
$current = @(Invoke-AzJson @('ad', 'app', 'federated-credential', 'list', '--id', $app.id)) | Where-Object name -eq $credentialName
if ($current -and $current.subject -eq $subjectClaim) { Write-Host "already exists, trusts $subjectClaim" }
else {
    $file = Join-Path ([IO.Path]::GetTempPath()) "$([guid]::NewGuid()).json"
    try {
        @{ name = $credentialName; issuer = 'https://token.actions.githubusercontent.com'; subject = $subjectClaim; audiences = @('api://AzureADTokenExchange') } |
            ConvertTo-Json | Set-Content $file -Encoding utf8
        if ($current) {
            Invoke-Native az @('ad', 'app', 'federated-credential', 'update', '--id', $app.id, '--federated-credential-id', $credentialName, '--parameters', "@$file") | Out-Null
            Write-Host "updated, now trusts $subjectClaim (was $($current.subject))"
        } else {
            Invoke-Native az @('ad', 'app', 'federated-credential', 'create', '--id', $app.id, '--parameters', "@$file") | Out-Null
            Write-Host "created, trusts $subjectClaim"
        }
    } finally { Remove-Item $file -ErrorAction SilentlyContinue }
}

Step "Role '$signerRole' on the certificate profile"
$assigned = @(Invoke-AzJson @('role', 'assignment', 'list', '--assignee', $principal.id, '--scope', $profileId, '--role', $signerRole))
if ($assigned.Count -ge 1) { Write-Host 'already assigned' }
else {
    Invoke-Native az @('role', 'assignment', 'create', '--assignee-object-id', $principal.id, '--assignee-principal-type', 'ServicePrincipal', '--role', $signerRole, '--scope', $profileId) | Out-Null
    Write-Host 'assigned (Azure can take a few minutes to honour a new assignment)'
}

Step 'Result for the GitHub half'
$result = [ordered]@{
    repo = $Repo; environment = $Environment
    variables = [ordered]@{ SIGNING_ENDPOINT = $endpoint; SIGNING_ACCOUNT = $AccountName; SIGNING_PROFILE = $ProfileName; SIGNING_EXPECTED_SUBJECT = $ExpectedSubject }
    secrets = [ordered]@{ AZURE_CLIENT_ID = $app.appId; AZURE_TENANT_ID = $tenantId; AZURE_SUBSCRIPTION_ID = $subscriptionId }
    deploymentBranches = $DeploymentBranches; deploymentTags = $DeploymentTags
}
# These are identifiers, not key material: nothing here can sign anything without a GitHub OIDC token for the trusted subject.
$result | ConvertTo-Json -Depth 4
Write-Host "Releases will sign as '$($signingProfile.properties.certificates[0].subjectName)'. Nothing has been signed yet."
