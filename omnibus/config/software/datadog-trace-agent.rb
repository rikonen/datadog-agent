# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https:#www.datadoghq.com/).
# Copyright 2017 Datadog, Inc.

require "./lib/ostools.rb"
require 'pathname'

name "datadog-trace-agent"

default_version "master"

source git: 'https://github.com/DataDog/datadog-trace-agent.git'
relative_path 'src/github.com/DataDog/datadog-trace-agent'

if windows?
  trace_agent_binary = "trace-agent.exe"
else
  trace_agent_binary = "trace-agent"
end

build do
  ship_license "https://raw.githubusercontent.com/DataDog/datadog-trace-agent/#{version}/LICENSE"

  # set GOPATH on the omnibus source dir for this software
  gopath = Pathname.new(project_dir) + '../../../..'
  if windows?
    env = {
        'GOPATH' => gopath.to_path,
        'PATH' => "#{gopath.to_path}/bin:#{ENV['PATH']}",
        'TRACE_AGENT_VERSION' => default_version, # used by gorake.rb in the trace-agent
        'WINDRES' => 'true',
    }
  else
    env = {
        'GOPATH' => gopath.to_path,
        'PATH' => "#{gopath.to_path}/bin:#{ENV['PATH']}",
        'TRACE_AGENT_VERSION' => default_version, # used by gorake.rb in the trace-agent
    }
  end

  command "go get github.com/Masterminds/glide", :env => env
  command "glide install", :env => env
  command "rake build", :env => env
  if windows?
    copy trace_agent_binary, "#{install_dir}/bin/agent"
  else
    copy trace_agent_binary, "#{install_dir}/embedded/bin"
  end
end
