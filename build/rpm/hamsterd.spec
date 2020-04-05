%define debug_package %{nil}
%bcond_without check
%global goipath         github.com/BOPOHA/hamsterd
Version:                v0.0.4

%global common_description %{expand:
Simple caching proxy for a fast rebuild containers.}

%global golicenses      LICENSE
%global godocs          README.md
%global goname          hamsterd
%define pathprefix      go/src
%define gobuilddir      %{_builddir}/%{pathprefix}/%{goipath}

Name:           %{goname}
Release:        1%{?dist}
Summary:        Simple caching proxy for a fast rebuild containers

License:        MIT
URL:            %{gourl}
Source0:        %{gosource}
BuildRequires:  golang-bin

%description
%{common_description}

%prep
%setup -T -cn %{pathprefix}/%{goipath}
tar -xvof %{SOURCE0} --strip 1
ls -lah

%build
make production

%install
install -m 0755 -vd                     %{buildroot}%{_bindir}
install -m 0755 -vp %{gobuilddir}/bin/* %{buildroot}%{_bindir}/

%if %{with check}
%check

%endif

%files
%license LICENSE
%doc README.md
%{_bindir}/*

%changelog
* Wed Apr 01 2020 Anatolii Vorona <vorona.tolik@gmail.com> - v0.0.3-1
- Initial package
