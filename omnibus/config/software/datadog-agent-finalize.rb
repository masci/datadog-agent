# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https:#www.datadoghq.com/).
# Copyright 2018 Datadog, Inc.

# This software definition doesn"t build anything, it"s the place where we create
# files outside the omnibus installation directory, so that we can add them to
# the package manifest using `extra_package_file` in the project definition.
require './lib/ostools.rb'

name "datadog-agent-finalize"
description "steps required to finalize the build"
default_version "1.0.0"
skip_transitive_dependency_licensing true

build do
    # TODO too many things done here, should be split
    block do
        # Conf files

        if windows?
            conf_dir_root = "#{Omnibus::Config.source_dir()}/etc/datadog-agent"
            conf_dir = "#{conf_dir_root}/extra_package_files/EXAMPLECONFSLOCATION"
            mkdir conf_dir
            move "#{install_dir}/etc/datadog-agent/datadog.yaml.example", conf_dir_root, :force=>true
            move "#{install_dir}/etc/datadog-agent/trace-agent.conf.example", conf_dir_root, :force=>true
            #move "#{install_dir}/etc/datadog-agent/process-agent.conf.example", conf_dir
            move "#{install_dir}/etc/datadog-agent/conf.d/*", conf_dir, :force=>true
            delete "#{install_dir}/bin/agent/agent.exe"
            # TODO why does this get generated at all
            delete "#{install_dir}/bin/agent/agent.exe~"

            # remove the config files for the subservices; they'll be started
            # based on the config file
            delete "#{conf_dir}/apm.yaml.default"
            delete "#{conf_dir}/process_agent.yaml.default"

            # cleanup clutter
            delete "#{install_dir}/etc"
            delete "#{install_dir}/bin/agent/dist/conf.d"
            delete "#{install_dir}/bin/agent/dist/*.conf*"
            delete "#{install_dir}/bin/agent/dist/*.yaml"
        elsif linux?
            # Move system service files
            mkdir "/etc/init"
            move "#{install_dir}/scripts/datadog-agent.conf", "/etc/init"
            mkdir "/lib/systemd/system"
            move "#{install_dir}/scripts/datadog-agent.service", "/lib/systemd/system"

            # Move checks and configuration files
            mkdir "/etc/datadog-agent"
            move "#{install_dir}/bin/agent/dd-agent", "/usr/bin/dd-agent"
            move "#{install_dir}/etc/datadog-agent/datadog.yaml.example", "/etc/datadog-agent"
            move "#{install_dir}/etc/datadog-agent/trace-agent.conf.example", "/etc/datadog-agent"
            move "#{install_dir}/etc/datadog-agent/process-agent.conf.example", "/etc/datadog-agent"
            move "#{install_dir}/etc/datadog-agent/conf.d", "/etc/datadog-agent", :force=>true

            # cleanup clutter
            delete "#{install_dir}/etc"
        elsif osx?
            # Nothing to move on osx, the confs already live in /opt/datadog-agent/etc/
        end
    end
end

