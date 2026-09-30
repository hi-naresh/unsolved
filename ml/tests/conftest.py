import os
import sys

# Make `app` importable when pytest runs from the repo root or from ml/.
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
