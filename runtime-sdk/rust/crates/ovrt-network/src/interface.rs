//! Network interface enumeration and explicit socket binding.
//!
//! A multipath lane needs two things the standard library does not offer: a
//! list of the paths this host actually has, and the ability to pin a socket to
//! one of them. Without the second, a "multipath" send is two sockets that the
//! routing table sends out of the same interface — two copies of a frame down
//! one wire, which costs double and buys nothing.
//!
//! Both are syscalls, and both are OS-specific in their spelling but not in
//! their meaning:
//!
//! - **Linux** binds with `SO_BINDTODEVICE`, taking the interface *name*. It
//!   required `CAP_NET_RAW` until Linux 5.7 and does not on anything current,
//!   which is why support here is probed rather than assumed from the OS name.
//! - **Darwin** binds with `IP_BOUND_IF`, taking the interface *index*. There is
//!   no name-based form.
//! - Everything else has neither, and reports so rather than pretending.
//!
//! Following `server-kit/go/kernellane`: a real path where the OS supports it, a
//! cached probe that tests behaviour rather than reading a flag, a fallback that
//! degrades instead of erroring, and no OS-specific type in the public API.
//!
//! Known gap, stated rather than hidden: on Darwin this binds the IPv4 option
//! only. `IPV6_BOUND_IF` is the v6 spelling and a v6 socket needs it instead.
//! The probe uses a v4 socket, so a host that reports support has been proven to
//! support exactly what this module does.

use std::sync::OnceLock;

use crate::{NetworkError, SocketFd};

/// A network interface this host can bind a socket to.
///
/// Both identifiers are carried because the two platforms want different ones:
/// Linux binds by name, Darwin by index. Callers pass the whole interface and
/// never learn which half was used.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NetworkInterface {
    name: String,
    index: u32,
    is_up: bool,
    is_running: bool,
    is_loopback: bool,
    is_point_to_point: bool,
}

impl NetworkInterface {
    /// The kernel's name for the interface, such as `eth0` or `en0`.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// The kernel's index for the interface. Never zero for a real interface.
    pub fn index(&self) -> u32 {
        self.index
    }

    /// Whether the interface is administratively up.
    pub fn is_up(&self) -> bool {
        self.is_up
    }

    /// Whether the interface has a live carrier.
    ///
    /// Distinct from [`is_up`](Self::is_up), and the distinction is the one that
    /// matters for path selection: an unplugged cable leaves the interface up
    /// and not running, and racing a frame down it wastes the send.
    pub fn is_running(&self) -> bool {
        self.is_running
    }

    /// Whether this is a loopback interface.
    pub fn is_loopback(&self) -> bool {
        self.is_loopback
    }

    /// Whether this is a point-to-point interface, such as a VPN or cellular
    /// link.
    pub fn is_point_to_point(&self) -> bool {
        self.is_point_to_point
    }

    /// Whether this interface is a plausible member of a race set.
    ///
    /// Up, carrying, and not loopback. Loopback is excluded because a frame
    /// raced against itself on the same host measures the scheduler, not the
    /// network — but it is still enumerated, because the binding probe needs an
    /// interface that is guaranteed to exist.
    pub fn is_usable_path(&self) -> bool {
        self.is_up && self.is_running && !self.is_loopback
    }
}

/// Lists the host's network interfaces, deduplicated by name.
///
/// `getifaddrs` reports one entry per *address*, so an interface holding both an
/// IPv4 and an IPv6 address appears twice. Paths are per-interface, not per
/// address family, so the duplicates are folded here rather than left for every
/// caller to fold identically.
///
/// Returns an empty list rather than an error on platforms without the syscall:
/// a host with no enumerable interfaces and a host that cannot enumerate them
/// are the same thing to a lane planner, which is that there is no multipath
/// set to plan over.
pub fn enumerate() -> Result<Vec<NetworkInterface>, NetworkError> {
    imp::enumerate()
}

/// Binds a socket to a specific interface, so its traffic leaves by that path
/// regardless of what the routing table would have chosen.
///
/// # Errors
///
/// Returns [`NetworkError::Unsupported`] on platforms with no binding
/// primitive, and [`NetworkError::Syscall`] when the kernel refuses — most
/// often because the interface disappeared between enumeration and the bind, or
/// because the process lacks the privilege an older kernel demanded.
///
/// `fd` must be an open socket owned by the caller for the duration of the call.
/// This is not an `unsafe fn` because a closed or foreign descriptor makes the
/// kernel return `EBADF` rather than causing undefined behaviour.
pub fn bind_socket_to_interface(
    fd: SocketFd,
    interface: &NetworkInterface,
) -> Result<(), NetworkError> {
    imp::bind(fd, interface)
}

/// Reports, with a cached one-shot probe, whether this host can actually pin a
/// socket to an interface.
///
/// The probe opens a UDP socket and binds it to the loopback interface. It tests
/// the behaviour rather than the platform, because the answer is not a property
/// of the OS alone: the same Linux binary is privileged enough on 5.7 and later
/// and may not be under an older kernel or a restrictive sandbox, and reading
/// `cfg!(target_os)` would report success in both cases.
///
/// Never errors. A host that cannot bind has one path, which is a smaller lane
/// planner problem, not a failure.
pub fn interface_binding_supported() -> bool {
    static PROBE: OnceLock<bool> = OnceLock::new();
    *PROBE.get_or_init(probe_interface_binding)
}

fn probe_interface_binding() -> bool {
    let Ok(interfaces) = enumerate() else {
        return false;
    };
    // Loopback specifically: it is the one interface every host has, it needs no
    // carrier, and binding to it sends nothing anywhere.
    let Some(loopback) = interfaces.iter().find(|candidate| candidate.is_loopback) else {
        return false;
    };
    let Ok(socket) = imp::ProbeSocket::open() else {
        return false;
    };
    bind_socket_to_interface(socket.fd(), loopback).is_ok()
}

// ---------------------------------------------------------------------------
// Unix implementation
// ---------------------------------------------------------------------------

#[cfg(unix)]
mod imp {
    use super::NetworkInterface;
    use crate::{NetworkError, SocketFd};
    use std::ffi::CStr;

    pub(super) fn enumerate() -> Result<Vec<NetworkInterface>, NetworkError> {
        let mut head: *mut libc::ifaddrs = std::ptr::null_mut();

        // SAFETY: `head` is a live, correctly typed out-parameter. On success
        // the kernel writes an owned linked list into it, which the guard below
        // frees exactly once; on failure it is left null and never read.
        let rc = unsafe { libc::getifaddrs(&mut head) };
        if rc != 0 {
            return Err(NetworkError::Syscall { call: "getifaddrs", errno: last_errno() });
        }

        // Frees the list even if the walk below returns early.
        let list = IfAddrsList { head };

        let mut interfaces: Vec<NetworkInterface> = Vec::new();
        let mut cursor = list.head;
        while !cursor.is_null() {
            // SAFETY: `cursor` is non-null and points into the list getifaddrs
            // allocated, which `list` still owns and has not yet freed.
            let entry = unsafe { &*cursor };
            cursor = entry.ifa_next;

            if entry.ifa_name.is_null() {
                continue;
            }
            // SAFETY: `ifa_name` is non-null and getifaddrs documents it as a
            // NUL-terminated string living in the same allocation as `entry`,
            // which outlives this borrow.
            let raw_name = unsafe { CStr::from_ptr(entry.ifa_name) };
            let Ok(name) = raw_name.to_str() else {
                // A non-UTF-8 interface name cannot be reported and cannot be
                // bound by name on Linux. Skipping it loses one path rather
                // than failing the whole enumeration.
                continue;
            };
            if interfaces.iter().any(|existing| existing.name == name) {
                continue;
            }

            let flags = entry.ifa_flags as libc::c_int;
            // SAFETY: `raw_name` is a valid NUL-terminated C string for the
            // duration of this call, which is all if_nametoindex requires.
            let index = unsafe { libc::if_nametoindex(raw_name.as_ptr()) };
            if index == 0 {
                // The interface vanished between the walk and the lookup. It is
                // not a path any more, so it is not one to report.
                continue;
            }

            interfaces.push(NetworkInterface {
                name: name.to_string(),
                index,
                is_up: flags & libc::IFF_UP != 0,
                is_running: flags & libc::IFF_RUNNING != 0,
                is_loopback: flags & libc::IFF_LOOPBACK != 0,
                is_point_to_point: flags & libc::IFF_POINTOPOINT != 0,
            });
        }

        Ok(interfaces)
    }

    /// Owns the linked list `getifaddrs` allocated, so the walk above can return
    /// early without leaking it.
    struct IfAddrsList {
        head: *mut libc::ifaddrs,
    }

    impl Drop for IfAddrsList {
        fn drop(&mut self) {
            if self.head.is_null() {
                return;
            }
            // SAFETY: `head` came from a successful getifaddrs and is freed
            // exactly once, here, because this type is neither Copy nor Clone.
            unsafe { libc::freeifaddrs(self.head) };
        }
    }

    #[cfg(target_os = "linux")]
    pub(super) fn bind(fd: SocketFd, interface: &NetworkInterface) -> Result<(), NetworkError> {
        // SO_BINDTODEVICE takes the name, not the index, and needs no NUL
        // terminator because the length is passed explicitly.
        let name = interface.name.as_bytes();

        // SAFETY: `fd` is a socket the caller owns; the value pointer addresses
        // `name`'s bytes and the length passed is exactly that slice's length,
        // so the kernel reads only initialised memory that outlives the call.
        let rc = unsafe {
            libc::setsockopt(
                fd,
                libc::SOL_SOCKET,
                libc::SO_BINDTODEVICE,
                name.as_ptr().cast::<libc::c_void>(),
                name.len() as libc::socklen_t,
            )
        };
        if rc != 0 {
            return Err(NetworkError::Syscall {
                call: "setsockopt(SO_BINDTODEVICE)",
                errno: last_errno(),
            });
        }
        Ok(())
    }

    #[cfg(target_vendor = "apple")]
    pub(super) fn bind(fd: SocketFd, interface: &NetworkInterface) -> Result<(), NetworkError> {
        // Darwin binds by index through IP_BOUND_IF. The constant is not exposed
        // by every libc release for every Apple target, so it is named here
        // against the value in <netinet/in.h>, stable since the option was
        // introduced.
        const IP_BOUND_IF: libc::c_int = 25;

        let index: libc::c_uint = interface.index;

        // SAFETY: `fd` is a socket the caller owns; the value pointer addresses
        // a live, initialised `c_uint` local and the length passed is that
        // type's size, so the kernel reads exactly the bytes that exist.
        let rc = unsafe {
            libc::setsockopt(
                fd,
                libc::IPPROTO_IP,
                IP_BOUND_IF,
                std::ptr::addr_of!(index).cast::<libc::c_void>(),
                std::mem::size_of::<libc::c_uint>() as libc::socklen_t,
            )
        };
        if rc != 0 {
            return Err(NetworkError::Syscall {
                call: "setsockopt(IP_BOUND_IF)",
                errno: last_errno(),
            });
        }
        Ok(())
    }

    #[cfg(not(any(target_os = "linux", target_vendor = "apple")))]
    pub(super) fn bind(_fd: SocketFd, _interface: &NetworkInterface) -> Result<(), NetworkError> {
        Err(NetworkError::Unsupported { what: "binding a socket to a named interface" })
    }

    /// A UDP socket that closes itself. Used only by the binding probe, which is
    /// why it opens `AF_INET`: that is the family the Darwin bind above targets,
    /// so a successful probe has exercised the real path.
    pub(super) struct ProbeSocket {
        fd: SocketFd,
    }

    impl ProbeSocket {
        pub(super) fn open() -> Result<Self, NetworkError> {
            // SAFETY: socket(2) with constant arguments. It either returns a
            // descriptor this value now owns, or -1, which is checked below.
            let fd = unsafe { libc::socket(libc::AF_INET, libc::SOCK_DGRAM, 0) };
            if fd < 0 {
                return Err(NetworkError::Syscall { call: "socket", errno: last_errno() });
            }
            Ok(Self { fd })
        }

        pub(super) fn fd(&self) -> SocketFd {
            self.fd
        }
    }

    impl Drop for ProbeSocket {
        fn drop(&mut self) {
            // SAFETY: `fd` came from a successful socket(2), is owned by this
            // value, and is closed exactly once because ProbeSocket is not Copy
            // and hands out no owning duplicate.
            unsafe { libc::close(self.fd) };
        }
    }

    fn last_errno() -> i32 {
        std::io::Error::last_os_error().raw_os_error().unwrap_or(0)
    }
}

// ---------------------------------------------------------------------------
// Non-unix stub
// ---------------------------------------------------------------------------

#[cfg(not(unix))]
mod imp {
    use super::NetworkInterface;
    use crate::{NetworkError, SocketFd};

    pub(super) fn enumerate() -> Result<Vec<NetworkInterface>, NetworkError> {
        // Not an error: a host whose interfaces cannot be enumerated has no
        // multipath set, which is a planning input rather than a failure.
        Ok(Vec::new())
    }

    pub(super) fn bind(_fd: SocketFd, _interface: &NetworkInterface) -> Result<(), NetworkError> {
        Err(NetworkError::Unsupported { what: "binding a socket to a named interface" })
    }

    pub(super) struct ProbeSocket;

    impl ProbeSocket {
        pub(super) fn open() -> Result<Self, NetworkError> {
            Err(NetworkError::Unsupported { what: "opening a probe socket" })
        }

        pub(super) fn fd(&self) -> SocketFd {
            -1
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_host_reports_a_loopback_interface() {
        let interfaces = enumerate().expect("enumeration should degrade, not fail");
        if interfaces.is_empty() {
            // The non-unix stub, or a sandbox with no interfaces at all.
            return;
        }
        assert!(
            interfaces.iter().any(NetworkInterface::is_loopback),
            "no loopback among {:?}",
            interfaces.iter().map(NetworkInterface::name).collect::<Vec<_>>()
        );
    }

    #[test]
    fn interfaces_are_deduplicated_by_name() {
        // getifaddrs reports one entry per address, so a dual-stack interface
        // arrives twice. This is the assertion that the fold happens here rather
        // than in every caller.
        let interfaces = enumerate().expect("enumeration should degrade, not fail");
        let mut names: Vec<&str> = interfaces.iter().map(NetworkInterface::name).collect();
        let before = names.len();
        names.sort_unstable();
        names.dedup();
        assert_eq!(before, names.len(), "getifaddrs duplicates were not folded");
    }

    #[test]
    fn every_reported_interface_has_a_nonzero_index() {
        let interfaces = enumerate().expect("enumeration should degrade, not fail");
        for interface in &interfaces {
            assert_ne!(interface.index(), 0, "{} reported index 0", interface.name());
        }
    }

    #[test]
    fn loopback_is_never_offered_as_a_race_path() {
        let interfaces = enumerate().expect("enumeration should degrade, not fail");
        for interface in interfaces.iter().filter(|candidate| candidate.is_loopback()) {
            assert!(!interface.is_usable_path(), "{} offered as a race path", interface.name());
        }
    }

    #[test]
    fn the_binding_probe_is_stable_across_calls() {
        // Cached in a OnceLock, so a second answer differing from the first
        // would mean the cache is not doing its job.
        assert_eq!(interface_binding_supported(), interface_binding_supported());
    }

    #[test]
    fn the_probe_agrees_with_a_real_bind() {
        // The probe's whole claim is that it reports what a real bind would do.
        // If these ever disagree, the probe is decorative.
        let interfaces = enumerate().expect("enumeration should degrade, not fail");
        let Some(loopback) = interfaces.iter().find(|candidate| candidate.is_loopback()) else {
            return;
        };
        let Ok(socket) = imp::ProbeSocket::open() else {
            return;
        };

        let outcome = bind_socket_to_interface(socket.fd(), loopback);
        assert_eq!(
            outcome.is_ok(),
            interface_binding_supported(),
            "probe and a real bind disagree: {outcome:?}"
        );
    }
}
