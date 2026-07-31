#!/usr/bin/env pwsh

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$pesterVersion = '5.7.1'

if (-not (Get-Module -ListAvailable -Name Pester |
        Where-Object { $_.Version -eq [version] $pesterVersion })) {
    Write-Host "installing Pester $pesterVersion"
    Install-Module -Name Pester `
        -RequiredVersion $pesterVersion `
        -Scope CurrentUser `
        -Force `
        -SkipPublisherCheck
}

Import-Module Pester -RequiredVersion $pesterVersion

$configuration = New-PesterConfiguration
$configuration.Run.Path = Join-Path $PSScriptRoot 'coverage-gate.Tests.ps1'
$configuration.Run.Exit = $true
$configuration.Output.Verbosity = 'Detailed'

Invoke-Pester -Configuration $configuration
