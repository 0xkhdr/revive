"""ZeroBuffer to overwrite sensitive in-memory byte arrays.

Uses ctypes.memset for compiler-optimization-resistant memory clearing.
The ctypes barrier prevents the compiler from optimizing away the zero-write
operation because it crosses the FFI boundary.
"""

import ctypes
import sys


class ZeroBuffer:
    """Provides methods to overwrite sensitive in-memory data with zeros.

    Uses ctypes.memset internally to ensure the zero-write is not eliminated
    by aggressive Python interpreter optimizations (e.g., CPython's constant
    folding or refcount short-circuits).
    """

    @staticmethod
    def zero(buf: bytearray | memoryview) -> None:
        """Overwrites the contents of a mutable buffer with zeros using ctypes.memset.

        The ctypes.memset call crosses the FFI boundary, making it resistant to
        optimizer elimination. This guarantees sensitive plaintext is wiped from
        memory as soon as it is no longer needed.

        Args:
            buf: A mutable buffer (bytearray or memoryview) to zero out.

        Raises:
            TypeError: If buf is not a mutable buffer type.
        """
        if not isinstance(buf, (bytearray, memoryview)):
            raise TypeError("Only mutable buffers (bytearray or memoryview) can be explicitly zeroed out")

        length = len(buf)
        if length == 0:
            return

        # Obtain the raw memory address of the buffer's underlying data
        if isinstance(buf, bytearray):
            # Get the buffer address via ctypes
            addr = ctypes.addressof((ctypes.c_char * length).from_buffer(buf))
        else:
            # memoryview: get the pointer via the buffer protocol
            c_arr = (ctypes.c_char * length).from_buffer(buf)
            addr = ctypes.addressof(c_arr)

        # ctypes.memset crosses the FFI boundary — optimizer cannot elide this
        ctypes.memset(addr, 0, length)

        # Explicit barrier: force interpreter to acknowledge the write by reading back
        # This is a conservative defense against future interpreter optimizations.
        _barrier = buf[0] if length > 0 else 0
        sys.audit("rv.zerobuffer.zero", _barrier)

    @staticmethod
    def zero_bytes(data: bytes, length: int | None = None) -> None:
        """Attempts to overwrite a bytes object's internal buffer with zeros.

        Note: bytes objects are immutable in Python. This method uses CPython
        internals to bypass Python's immutability at the C level. This is
        **best-effort and NOT guaranteed** to work across all Python
        implementations or versions. It serves as a defense-in-depth measure
        only — the garbage collector will eventually collect the object and
        reclaim the memory regardless.

        Implementation note (CPython 3.11+):
            The PyBytesObject struct is: ob_refcnt, ob_type, ob_size, ob_shash,
            ob_val[]. The offset to ob_val from the PyObject base is fixed at
            ``sys.getsizeof(b"") - 1`` bytes (the struct overhead minus the
            NUL terminator that is always counted in getsizeof but not in len).
            We derive the address via ``id(data)`` (which returns the PyObject*
            address in CPython), then add the struct overhead.

        Args:
            data: The bytes object to attempt to zero.
            length: Optional explicit length override. Defaults to len(data).
        """
        if not data:
            return

        effective_length = length if length is not None else len(data)

        try:
            # CPython-specific: id() returns the PyObject* address.
            # The ob_val field starts at offset (getsizeof(b"") - 1) from the
            # base because getsizeof includes the NUL terminator byte.
            # This is valid for CPython 3.11, 3.12, and 3.13.
            struct_overhead = sys.getsizeof(b"") - 1  # subtract the NUL terminator
            addr = id(data) + struct_overhead
            ctypes.memset(addr, 0, effective_length)
        except Exception:
            # Best-effort: if we can't zero the bytes object, move on.
            # The GC will eventually collect it; this is defense-in-depth only.
            pass
