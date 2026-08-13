#!/usr/bin/env pwsh

[CmdletBinding()]
param(
    [double] $Threshold = 0,
    [string[]] $Report = @('coverage-go.lcov', 'coverage-swift.lcov'),
    [string] $Exclusions = 'coverage-exclude.txt',
    [string] $Merged = 'coverage.lcov'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Test-GlobSegments {
    param(
        [string[]] $Pattern,
        [string[]] $Path,
        [int] $PatternIndex = 0,
        [int] $PathIndex = 0
    )

    if ($PatternIndex -ge $Pattern.Count) { return $PathIndex -ge $Path.Count }
    if ($Pattern[$PatternIndex] -eq '**') {
        if (Test-GlobSegments $Pattern $Path ($PatternIndex + 1) $PathIndex) { return $true }
        if ($PathIndex -lt $Path.Count) {
            return Test-GlobSegments $Pattern $Path $PatternIndex ($PathIndex + 1)
        }
        return $false
    }
    if ($PathIndex -ge $Path.Count) { return $false }
    if ($Path[$PathIndex] -notlike $Pattern[$PatternIndex]) { return $false }
    return Test-GlobSegments $Pattern $Path ($PatternIndex + 1) ($PathIndex + 1)
}

function Test-GlobMatch {
    param([string] $Pattern, [string] $Path)

    if ($Pattern.StartsWith('/')) {
        return Test-GlobSegments $Pattern.Substring(1).Split('/') $Path.Split('/')
    }
    if (-not $Pattern.Contains('/')) {
        return (Split-Path -Path $Path -Leaf) -like $Pattern
    }
    return Test-GlobSegments $Pattern.Split('/') $Path.Split('/')
}

function Test-Excluded {
    param([string[]] $Patterns, [string] $Path)

    foreach ($pattern in $Patterns) {
        if (Test-GlobMatch -Pattern $pattern -Path $Path) { return $true }
    }
    return $false
}

function Read-Exclusions {
    param([string[]] $Lines)

    return @($Lines |
        ForEach-Object { $_.Trim() } |
        Where-Object { $_ -ne '' -and -not $_.StartsWith('#') })
}

function Read-Lcov {
    param([string[]] $Lines)

    $byFile = [ordered]@{}
    $current = $null
    foreach ($raw in $Lines) {
        $line = $raw.Trim()
        if ($line.StartsWith('SF:')) {
            $file = $line.Substring(3).Replace('\', '/')
            if (-not $byFile.Contains($file)) {
                $byFile[$file] = [pscustomobject]@{ File = $file; Lines = 0; Covered = 0 }
            }
            $current = $byFile[$file]
        }
        elseif ($line.StartsWith('DA:')) {
            if ($null -eq $current) {
                throw "coverage-gate: DA record before any SF record: '$line'"
            }
            $fields = $line.Substring(3).Split(',')
            if ($fields.Count -lt 2) {
                throw "coverage-gate: malformed DA record: '$line'"
            }
            $hits = 0.0
            if (-not [double]::TryParse($fields[1], [ref] $hits)) {
                throw "coverage-gate: bad hit count in '$line'"
            }
            $current.Lines++
            if ($hits -gt 0) { $current.Covered++ }
        }
    }
    return @($byFile.Values)
}

function Get-GoPackages {
    param([string] $RepoRoot)

    $format = "{{.Dir}}`t{{join .GoFiles `",`"}}`t{{len .TestGoFiles}}`t{{len .XTestGoFiles}}"
    $output = & go list -f $format ./...
    if ($LASTEXITCODE -ne 0) {
        throw "coverage-gate: go list failed with exit code $LASTEXITCODE"
    }

    return @($output | Where-Object { $_ -ne '' } | ForEach-Object {
            $fields = $_.Split("`t")
            $relative = [System.IO.Path]::GetRelativePath($RepoRoot, $fields[0]).Replace('\', '/')
            [pscustomobject]@{
                Dir           = $relative
                GoFiles       = @($fields[1].Split(',') | Where-Object { $_ -ne '' })
                TestFileCount = [int] $fields[2] + [int] $fields[3]
            }
        })
}

function Invoke-Gate {
    param(
        [object[]] $Packages,
        [object[]] $Files,
        [string[]] $ExclusionPatterns,
        [double] $Floor
    )

    $byDir = [ordered]@{}
    foreach ($file in $Files) {
        if (Test-Excluded -Patterns $ExclusionPatterns -Path $file.File) { continue }
        $dir = if ($file.File.Contains('/')) { $file.File.Substring(0, $file.File.LastIndexOf('/')) } else { '.' }
        if (-not $byDir.Contains($dir)) {
            $byDir[$dir] = [pscustomobject]@{ Lines = 0; Covered = 0 }
        }
        $byDir[$dir].Lines += $file.Lines
        $byDir[$dir].Covered += $file.Covered
    }

    $results = [System.Collections.Generic.List[object]]::new()
    $claimed = [System.Collections.Generic.HashSet[string]]::new()

    foreach ($package in $Packages) {
        [void] $claimed.Add($package.Dir)

        $coverable = @($package.GoFiles | Where-Object {
                $relative = if ($package.Dir -eq '.') { $_ } else { "$($package.Dir)/$_" }
                -not (Test-Excluded -Patterns $ExclusionPatterns -Path $relative)
            })

        if ($coverable.Count -eq 0) {
            $results.Add((New-Result -Dir $package.Dir -Excluded)) | Out-Null
            continue
        }
        if ($package.TestFileCount -eq 0) {
            $results.Add((New-Result -Dir $package.Dir -NoTests)) | Out-Null
            continue
        }
        $results.Add((New-Result -Dir $package.Dir -Totals $byDir[$package.Dir] -Floor $Floor)) | Out-Null
    }

    foreach ($dir in $byDir.Keys) {
        if ($claimed.Contains($dir)) { continue }
        $results.Add((New-Result -Dir $dir -Totals $byDir[$dir] -Floor $Floor)) | Out-Null
    }

    return @($results | Sort-Object -Property Percent, Dir)
}

function New-Result {
    param(
        [string] $Dir,
        [object] $Totals,
        [double] $Floor,
        [switch] $Excluded,
        [switch] $NoTests
    )

    $lines = if ($Totals) { $Totals.Lines } else { 0 }
    $covered = if ($Totals) { $Totals.Covered } else { 0 }
    $percent = if ($lines -gt 0) { 100 * $covered / $lines } else { 0 }

    return [pscustomobject]@{
        Dir      = $Dir
        Percent  = if ($Excluded -or $NoTests) { 0 } else { $percent }
        Lines    = if ($Excluded -or $NoTests) { 0 } else { $lines }
        Covered  = if ($Excluded -or $NoTests) { 0 } else { $covered }
        Excluded = [bool] $Excluded
        NoTests  = [bool] $NoTests
        Pass     = if ($Excluded) { $true } elseif ($NoTests) { $false } else { $percent -ge $Floor }
    }
}

function Get-Half {
    param([string] $Dir)
    if ($Dir -eq 'macos' -or $Dir.StartsWith('macos/')) { return 'Swift' }
    return 'Go'
}

function Write-Report {
    param([object[]] $Results, [double] $Floor)

    $gated = @($Results | Where-Object { -not $_.Excluded })
    foreach ($result in $gated) {
        if ($result.NoTests) {
            [Console]::Out.WriteLine('  0.0%  {0}  (no test files)' -f $result.Dir)
        }
        else {
            [Console]::Out.WriteLine(('{0,5:F1}%  {1}' -f $result.Percent, $result.Dir))
        }
    }

    [Console]::Out.WriteLine('-------')
    $totalLines = 0
    $totalCovered = 0
    foreach ($group in $gated | Group-Object { Get-Half $_.Dir } | Sort-Object Name) {
        $lines = ($group.Group | Measure-Object -Property Lines -Sum).Sum
        $covered = ($group.Group | Measure-Object -Property Covered -Sum).Sum
        $totalLines += $lines
        $totalCovered += $covered
        $percent = if ($lines -gt 0) { 100 * $covered / $lines } else { 0 }
        [Console]::Out.WriteLine(('{0,5:F1}%  {1} subtotal ({2}/{3} lines)' -f $percent, $group.Name, $covered, $lines))
    }
    $total = if ($totalLines -gt 0) { 100 * $totalCovered / $totalLines } else { 0 }
    [Console]::Out.WriteLine(('{0,5:F1}%  MERGED TOTAL ({1}/{2} lines)' -f $total, $totalCovered, $totalLines))

    $failing = @($gated | Where-Object { -not $_.Pass })
    if ($failing.Count -eq 0) { return 0 }

    [Console]::Error.WriteLine(('FAIL: {0} package(s) below the {1}% per-package floor:' -f $failing.Count, $Floor))
    foreach ($result in $failing) {
        $detail = if ($result.NoTests) { 'no test files' } else { '{0:F1}%' -f $result.Percent }
        [Console]::Error.WriteLine(('  {0}: {1}' -f $result.Dir, $detail))
    }
    return 1
}

function Invoke-CoverageGate {
    param(
        [double] $Floor,
        [string[]] $ReportPaths,
        [string] $ExclusionsPath,
        [string] $MergedPath
    )

    $repoRoot = Split-Path -Path $PSScriptRoot -Parent
    Push-Location $repoRoot
    try {
        foreach ($path in $ReportPaths) {
            if (-not (Test-Path -Path $path -PathType Leaf)) {
                Write-Error -Message "error: $path not found — run 'mise run coverage' to build both halves" -ErrorAction Continue
                return 1
            }
        }

        $lcov = @($ReportPaths | ForEach-Object { Get-Content -Path $_ })
        Set-Content -Path $MergedPath -Value $lcov

        $results = Invoke-Gate `
            -Packages (Get-GoPackages -RepoRoot $repoRoot) `
            -Files (Read-Lcov -Lines $lcov) `
            -ExclusionPatterns (Read-Exclusions -Lines (Get-Content -Path $ExclusionsPath)) `
            -Floor $Floor

        return Write-Report -Results $results -Floor $Floor
    }
    finally {
        Pop-Location
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    exit (Invoke-CoverageGate -Floor $Threshold -ReportPaths $Report -ExclusionsPath $Exclusions -MergedPath $Merged)
}
