"""ZeroBuffer to overwrite sensitive in-memory byte arrays.
"""



class ZeroBuffer:
    """Provides methods to overwrite sensitive in-memory data with zeros."""

    @staticmethod
    def zero(buf: bytearray | memoryview) -> None:
        """Overwrites the contents of a mutable buffer with zeros.

        This guarantees that sensitive plaintext is wiped from memory as soon as it is no longer needed.
        """
        if isinstance(buf, (bytearray, memoryview)):
            for i in range(len(buf)):
                buf[i] = 0
        else:
            raise TypeError("Only mutable buffers (bytearray or memoryview) can be explicitly zeroed out")
