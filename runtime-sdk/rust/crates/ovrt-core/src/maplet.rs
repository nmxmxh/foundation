//! Space-efficient cuckoo maplets for high-performance runtime lanes.
//!
//! Provides zero-allocation approximate key-value mapping and saturating frequency counters.
//! Sized for micro-values and route caches with low alias collision rates.

/// 4 slots per bucket in cuckoo layout.
pub const MAPLET_SLOTS_PER_BUCKET: usize = 4;
/// Maximum cuckoo displacement kicks before reporting cycle.
pub const MAPLET_MAX_KICKS: usize = 500;

/// Bitwise hash mixer for 64-bit integer keys.
#[inline(always)]
pub fn maplet_hash64(key: u64) -> u64 {
    let mut z = key.wrapping_add(0x9E37_79B9_7F4A_7C15);
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
    z ^ (z >> 31)
}

/// Bitwise hash mixer for string keys.
#[inline(always)]
pub fn maplet_hash_str(key: &str) -> u64 {
    let mut h: u64 = 0xcbf29ce484222325;
    for &byte in key.as_bytes() {
        h ^= u64::from(byte);
        h = h.wrapping_mul(0x100000001b3);
    }
    h ^= h >> 30;
    h = h.wrapping_mul(0xbf58476d1ce4e5b9);
    h ^= h >> 27;
    h = h.wrapping_mul(0x94d049bb133111eb);
    h ^ (h >> 31)
}

/// Helper for power-of-two bucket index computation.
#[inline(always)]
pub fn bucket_index<const BUCKETS: usize>(val: usize) -> usize {
    if BUCKETS.is_power_of_two() {
        val & (BUCKETS - 1)
    } else {
        val % BUCKETS
    }
}

/// Secondary fingerprint hash to derive alternate bucket index.
#[inline(always)]
pub fn hash_fingerprint32(fp: u32) -> u32 {
    let mut v = fp ^ (fp >> 16);
    v = v.wrapping_mul(0x7feb352d);
    v ^= v >> 15;
    v = v.wrapping_mul(0x846ca68b);
    v ^ (v >> 16)
}

/// WideMaplet packs a 28-bit fingerprint and 4-bit micro-value into a 32-bit slot.
/// Sized for route tags and chunk indices with low alias collisions.
#[derive(Clone, Copy, Debug)]
pub struct WideMaplet<const BUCKETS: usize> {
    pub buckets: [[u32; MAPLET_SLOTS_PER_BUCKET]; BUCKETS],
    pub count: u32,
}

impl<const BUCKETS: usize> Default for WideMaplet<BUCKETS> {
    fn default() -> Self {
        Self {
            buckets: [[0; MAPLET_SLOTS_PER_BUCKET]; BUCKETS],
            count: 0,
        }
    }
}

impl<const BUCKETS: usize> WideMaplet<BUCKETS> {
    pub const FINGERPRINT_BITS: u32 = 28;
    pub const VALUE_BITS: u32 = 4;
    pub const MAX_FINGERPRINT: u32 = (1 << Self::FINGERPRINT_BITS) - 1;
    pub const MAX_VALUE: u8 = (1 << Self::VALUE_BITS) - 1;

    pub fn new() -> Self {
        Self::default()
    }

    #[inline(always)]
    fn decompose_hash(&self, h: u64) -> (usize, u32, usize) {
        let b1 = bucket_index::<BUCKETS>(h as usize);
        let mut fp = ((h >> 32) as u32) & Self::MAX_FINGERPRINT;
        if fp == 0 {
            fp = 1;
        }
        let fp_hash = hash_fingerprint32(fp);
        let b2 = bucket_index::<BUCKETS>(b1 ^ (fp_hash as usize));
        (b1, fp, b2)
    }

    #[inline(always)]
    fn pack_slot(fp: u32, val: u8) -> u32 {
        (fp << Self::VALUE_BITS) | u32::from(val & Self::MAX_VALUE)
    }

    #[inline(always)]
    fn unpack_slot(slot: u32) -> (u32, u8) {
        (slot >> Self::VALUE_BITS, (slot & u32::from(Self::MAX_VALUE)) as u8)
    }

    /// Inserts or updates an 8-bit key hash with a 4-bit micro-value.
    pub fn insert_hash(&mut self, h: u64, val: u8) -> bool {
        let (b1, fp, b2) = self.decompose_hash(h);
        let entry = Self::pack_slot(fp, val);

        // Check if existing fingerprint matches in b1.
        for slot in &mut self.buckets[b1] {
            if *slot != 0 && (*slot >> Self::VALUE_BITS) == fp {
                *slot = entry;
                return true;
            }
        }
        // Check if existing fingerprint matches in b2.
        for slot in &mut self.buckets[b2] {
            if *slot != 0 && (*slot >> Self::VALUE_BITS) == fp {
                *slot = entry;
                return true;
            }
        }

        // Insert into first empty slot in b1.
        for slot in &mut self.buckets[b1] {
            if *slot == 0 {
                *slot = entry;
                self.count += 1;
                return true;
            }
        }
        // Insert into first empty slot in b2.
        for slot in &mut self.buckets[b2] {
            if *slot == 0 {
                *slot = entry;
                self.count += 1;
                return true;
            }
        }

        // Cuckoo displacement loop.
        let mut curr_bucket = b1;
        let mut curr_entry = entry;
        for kick in 0..MAPLET_MAX_KICKS {
            let slot_idx = kick % MAPLET_SLOTS_PER_BUCKET;
            let victim = self.buckets[curr_bucket][slot_idx];
            self.buckets[curr_bucket][slot_idx] = curr_entry;

            let (v_fp, _) = Self::unpack_slot(victim);
            let v_hash = hash_fingerprint32(v_fp);
            curr_bucket = bucket_index::<BUCKETS>(curr_bucket ^ (v_hash as usize));
            curr_entry = victim;

            for slot in &mut self.buckets[curr_bucket] {
                if *slot == 0 {
                    *slot = curr_entry;
                    self.count += 1;
                    return true;
                }
            }
        }
        false
    }

    /// Inserts a string key with a 4-bit micro-value.
    pub fn insert(&mut self, key: &str, val: u8) -> bool {
        self.insert_hash(maplet_hash_str(key), val)
    }

    /// Queries the WideMaplet by key hash.
    #[inline(always)]
    pub fn lookup_hash(&self, h: u64) -> Option<u8> {
        let (b1, fp, b2) = self.decompose_hash(h);

        for &slot in &self.buckets[b1] {
            if slot != 0 && (slot >> Self::VALUE_BITS) == fp {
                return Some((slot & u32::from(Self::MAX_VALUE)) as u8);
            }
        }
        for &slot in &self.buckets[b2] {
            if slot != 0 && (slot >> Self::VALUE_BITS) == fp {
                return Some((slot & u32::from(Self::MAX_VALUE)) as u8);
            }
        }
        None
    }

    /// Queries the WideMaplet by string key.
    pub fn lookup(&self, key: &str) -> Option<u8> {
        self.lookup_hash(maplet_hash_str(key))
    }

    /// Returns the active item count.
    pub fn len(&self) -> usize {
        self.count as usize
    }

    /// Returns true when empty.
    pub fn is_empty(&self) -> bool {
        self.count == 0
    }
}

/// CountingMaplet provides space-efficient frequency counting with saturating counters.
/// Slot layout: 24-bit fingerprint and 8-bit saturating counter (0..255).
#[derive(Clone, Copy, Debug)]
pub struct CountingMaplet<const BUCKETS: usize> {
    pub buckets: [[u32; MAPLET_SLOTS_PER_BUCKET]; BUCKETS],
    pub count: u32,
}

impl<const BUCKETS: usize> Default for CountingMaplet<BUCKETS> {
    fn default() -> Self {
        Self {
            buckets: [[0; MAPLET_SLOTS_PER_BUCKET]; BUCKETS],
            count: 0,
        }
    }
}

impl<const BUCKETS: usize> CountingMaplet<BUCKETS> {
    pub const FINGERPRINT_BITS: u32 = 24;
    pub const MAX_FINGERPRINT: u32 = (1 << Self::FINGERPRINT_BITS) - 1;
    pub const MAX_COUNT: u8 = 255;

    pub fn new() -> Self {
        Self::default()
    }

    #[inline(always)]
    fn decompose_hash(&self, h: u64) -> (usize, u32, usize) {
        let b1 = bucket_index::<BUCKETS>(h as usize);
        let mut fp = ((h >> 32) as u32) & Self::MAX_FINGERPRINT;
        if fp == 0 {
            fp = 1;
        }
        let fp_hash = hash_fingerprint32(fp);
        let b2 = bucket_index::<BUCKETS>(b1 ^ (fp_hash as usize));
        (b1, fp, b2)
    }

    #[inline(always)]
    fn pack_slot(fp: u32, val: u8) -> u32 {
        (fp << 8) | u32::from(val)
    }

    #[inline(always)]
    fn unpack_slot(slot: u32) -> (u32, u8) {
        (slot >> 8, (slot & 0xFF) as u8)
    }

    /// Increments key count, saturating at 255, and returns the new count.
    pub fn increment_hash(&mut self, h: u64) -> u8 {
        let (b1, fp, b2) = self.decompose_hash(h);

        for slot in &mut self.buckets[b1] {
            if *slot != 0 && (*slot >> 8) == fp {
                let (_, curr) = Self::unpack_slot(*slot);
                let next = curr.saturating_add(1);
                *slot = Self::pack_slot(fp, next);
                return next;
            }
        }
        for slot in &mut self.buckets[b2] {
            if *slot != 0 && (*slot >> 8) == fp {
                let (_, curr) = Self::unpack_slot(*slot);
                let next = curr.saturating_add(1);
                *slot = Self::pack_slot(fp, next);
                return next;
            }
        }

        // Insert new entry with count 1 in b1.
        let entry = Self::pack_slot(fp, 1);
        for slot in &mut self.buckets[b1] {
            if *slot == 0 {
                *slot = entry;
                self.count += 1;
                return 1;
            }
        }
        // Insert new entry with count 1 in b2.
        for slot in &mut self.buckets[b2] {
            if *slot == 0 {
                *slot = entry;
                self.count += 1;
                return 1;
            }
        }

        // Cuckoo displacement.
        let mut curr_bucket = b1;
        let mut curr_entry = entry;
        for kick in 0..MAPLET_MAX_KICKS {
            let slot_idx = kick % MAPLET_SLOTS_PER_BUCKET;
            let victim = self.buckets[curr_bucket][slot_idx];
            self.buckets[curr_bucket][slot_idx] = curr_entry;

            let (v_fp, _) = Self::unpack_slot(victim);
            let v_hash = hash_fingerprint32(v_fp);
            curr_bucket = bucket_index::<BUCKETS>(curr_bucket ^ (v_hash as usize));
            curr_entry = victim;

            for slot in &mut self.buckets[curr_bucket] {
                if *slot == 0 {
                    *slot = curr_entry;
                    self.count += 1;
                    return 1;
                }
            }
        }
        // Table saturated; return saturated limit.
        Self::MAX_COUNT
    }

    /// Increments frequency count for a string key.
    pub fn increment(&mut self, key: &str) -> u8 {
        self.increment_hash(maplet_hash_str(key))
    }

    /// Queries current count for a key hash.
    #[inline(always)]
    pub fn count_hash(&self, h: u64) -> u8 {
        let (b1, fp, b2) = self.decompose_hash(h);
        for &slot in &self.buckets[b1] {
            if slot != 0 && (slot >> 8) == fp {
                return (slot & 0xFF) as u8;
            }
        }
        for &slot in &self.buckets[b2] {
            if slot != 0 && (slot >> 8) == fp {
                return (slot & 0xFF) as u8;
            }
        }
        0
    }

    /// Queries current count for a string key.
    pub fn count(&self, key: &str) -> u8 {
        self.count_hash(maplet_hash_str(key))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_wide_maplet_roundtrip() {
        let mut maplet = WideMaplet::<64>::new();
        assert!(maplet.insert("route.preview", 3));
        assert!(maplet.insert("route.compute", 7));
        assert!(maplet.insert("route.render", 15));

        assert_eq!(maplet.lookup("route.preview"), Some(3));
        assert_eq!(maplet.lookup("route.compute"), Some(7));
        assert_eq!(maplet.lookup("route.render"), Some(15));
        assert_eq!(maplet.lookup("route.absent"), None);

        // Update existing key
        assert!(maplet.insert("route.preview", 4));
        assert_eq!(maplet.lookup("route.preview"), Some(4));
    }

    #[test]
    fn test_counting_maplet_saturation() {
        let mut maplet = CountingMaplet::<32>::new();
        assert_eq!(maplet.count("user:123"), 0);

        assert_eq!(maplet.increment("user:123"), 1);
        assert_eq!(maplet.increment("user:123"), 2);
        assert_eq!(maplet.count("user:123"), 2);

        assert_eq!(maplet.increment("user:456"), 1);
        assert_eq!(maplet.count("user:456"), 1);
    }
}
